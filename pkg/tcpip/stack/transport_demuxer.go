// Copyright 2018 The gVisor Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package stack

import (
	"container/heap"
	"fmt"
	"math/rand"

	"gvisor.dev/gvisor/pkg/sync"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/hash/jenkins"
	"gvisor.dev/gvisor/pkg/tcpip/header"
)

type protocolIDs struct {
	network   tcpip.NetworkProtocolNumber
	transport tcpip.TransportProtocolNumber
}

// transportEndpoints manages all endpoints of a given protocol. It has its own
// mutex so as to reduce interference between protocols.
type transportEndpoints struct {
	// mu protects all fields of the transportEndpoints.
	mu        sync.RWMutex
	endpoints map[TransportEndpointID]*endpointsByNic
	// rawEndpoints contains endpoints for raw sockets, which receive all
	// traffic of a given protocol regardless of port.
	rawEndpoints []RawTransportEndpoint
}

// unregisterEndpoint unregisters the endpoint with the given id such that it
// won't receive any more packets.
func (eps *transportEndpoints) unregisterEndpoint(id TransportEndpointID, ep TransportEndpoint, bindToDevice tcpip.NICID) {
	eps.mu.Lock()
	defer eps.mu.Unlock()
	epsByNic, ok := eps.endpoints[id]
	if !ok {
		return
	}
	if !epsByNic.unregisterEndpoint(bindToDevice, ep) {
		return
	}
	delete(eps.endpoints, id)
}

func (eps *transportEndpoints) transportEndpoints() []TransportEndpoint {
	eps.mu.RLock()
	defer eps.mu.RUnlock()
	es := make([]TransportEndpoint, 0, len(eps.endpoints))
	for _, e := range eps.endpoints {
		es = append(es, e.transportEndpoints()...)
	}
	return es
}

type endpointsByNic struct {
	mu        sync.RWMutex
	endpoints map[tcpip.NICID]*multiPortEndpoint
	// seed is a random secret for a jenkins hash.
	seed uint32
}

func (epsByNic *endpointsByNic) transportEndpoints() []TransportEndpoint {
	epsByNic.mu.RLock()
	defer epsByNic.mu.RUnlock()
	var eps []TransportEndpoint
	for _, ep := range epsByNic.endpoints {
		eps = append(eps, ep.transportEndpoints()...)
	}
	return eps
}

// HandlePacket is called by the stack when new packets arrive to this transport
// endpoint.
func (epsByNic *endpointsByNic) handlePacket(r *Route, id TransportEndpointID, pkt PacketBuffer) {
	epsByNic.mu.RLock()

	mpep, ok := epsByNic.endpoints[r.ref.nic.ID()]
	if !ok {
		if mpep, ok = epsByNic.endpoints[0]; !ok {
			epsByNic.mu.RUnlock() // Don't use defer for performance reasons.
			return
		}
	}

	// If this is a broadcast or multicast datagram, deliver the datagram to all
	// endpoints bound to the right device.
	if isMulticastOrBroadcast(id.LocalAddress) {
		mpep.handlePacketAll(r, id, pkt)
		epsByNic.mu.RUnlock() // Don't use defer for performance reasons.
		return
	}
	// multiPortEndpoints are guaranteed to have at least one element.
	transEP := selectEndpoint(id, mpep, epsByNic.seed)
	if queuedProtocol, mustQueue := mpep.demux.queuedProtocols[protocolIDs{mpep.netProto, mpep.transProto}]; mustQueue {
		queuedProtocol.QueuePacket(r, transEP, id, pkt)
		epsByNic.mu.RUnlock()
		return
	}

	transEP.HandlePacket(r, id, pkt)
	epsByNic.mu.RUnlock() // Don't use defer for performance reasons.
}

// HandleControlPacket implements stack.TransportEndpoint.HandleControlPacket.
func (epsByNic *endpointsByNic) handleControlPacket(n *NIC, id TransportEndpointID, typ ControlType, extra uint32, pkt PacketBuffer) {
	epsByNic.mu.RLock()
	defer epsByNic.mu.RUnlock()

	mpep, ok := epsByNic.endpoints[n.ID()]
	if !ok {
		mpep, ok = epsByNic.endpoints[0]
	}
	if !ok {
		return
	}

	// TODO(eyalsoha): Why don't we look at id to see if this packet needs to
	// broadcast like we are doing with handlePacket above?

	// multiPortEndpoints are guaranteed to have at least one element.
	selectEndpoint(id, mpep, epsByNic.seed).HandleControlPacket(id, typ, extra, pkt)
}

// registerEndpoint returns true if it succeeds. It fails and returns
// false if ep already has an element with the same key.
func (epsByNic *endpointsByNic) registerEndpoint(d *transportDemuxer, netProto tcpip.NetworkProtocolNumber, transProto tcpip.TransportProtocolNumber, t TransportEndpoint, reusePort bool, bindToDevice tcpip.NICID) *tcpip.Error {
	epsByNic.mu.Lock()
	defer epsByNic.mu.Unlock()

	multiPortEp, ok := epsByNic.endpoints[bindToDevice]
	if !ok {
		multiPortEp = &multiPortEndpoint{
			demux:      d,
			netProto:   netProto,
			transProto: transProto,
			reuse:      reusePort,
		}
		epsByNic.endpoints[bindToDevice] = multiPortEp
	}

	return multiPortEp.singleRegisterEndpoint(t, reusePort)
}

// unregisterEndpoint returns true if endpointsByNic has to be unregistered.
func (epsByNic *endpointsByNic) unregisterEndpoint(bindToDevice tcpip.NICID, t TransportEndpoint) bool {
	epsByNic.mu.Lock()
	defer epsByNic.mu.Unlock()
	multiPortEp, ok := epsByNic.endpoints[bindToDevice]
	if !ok {
		return false
	}
	if multiPortEp.unregisterEndpoint(t) {
		delete(epsByNic.endpoints, bindToDevice)
	}
	return len(epsByNic.endpoints) == 0
}

// transportDemuxer demultiplexes packets targeted at a transport endpoint
// (i.e., after they've been parsed by the network layer). It does two levels
// of demultiplexing: first based on the network and transport protocols, then
// based on endpoints IDs. It should only be instantiated via
// newTransportDemuxer.
type transportDemuxer struct {
	// protocol is immutable.
	protocol        map[protocolIDs]*transportEndpoints
	queuedProtocols map[protocolIDs]queuedTransportProtocol
}

// queuedTransportProtocol if supported by a protocol implementation will cause
// the dispatcher to delivery packets to the QueuePacket method instead of
// calling HandlePacket directly on the endpoint.
type queuedTransportProtocol interface {
	QueuePacket(r *Route, ep TransportEndpoint, id TransportEndpointID, pkt PacketBuffer)
}

func newTransportDemuxer(stack *Stack) *transportDemuxer {
	d := &transportDemuxer{
		protocol:        make(map[protocolIDs]*transportEndpoints),
		queuedProtocols: make(map[protocolIDs]queuedTransportProtocol),
	}

	// Add each network and transport pair to the demuxer.
	for netProto := range stack.networkProtocols {
		for proto := range stack.transportProtocols {
			protoIDs := protocolIDs{netProto, proto}
			d.protocol[protoIDs] = &transportEndpoints{
				endpoints: make(map[TransportEndpointID]*endpointsByNic),
			}
			qTransProto, isQueued := (stack.transportProtocols[proto].proto).(queuedTransportProtocol)
			if isQueued {
				d.queuedProtocols[protoIDs] = qTransProto
			}
		}
	}

	return d
}

// registerEndpoint registers the given endpoint with the dispatcher such that
// packets that match the endpoint ID are delivered to it.
func (d *transportDemuxer) registerEndpoint(netProtos []tcpip.NetworkProtocolNumber, protocol tcpip.TransportProtocolNumber, id TransportEndpointID, ep TransportEndpoint, reusePort bool, bindToDevice tcpip.NICID) *tcpip.Error {
	for i, n := range netProtos {
		if err := d.singleRegisterEndpoint(n, protocol, id, ep, reusePort, bindToDevice); err != nil {
			d.unregisterEndpoint(netProtos[:i], protocol, id, ep, bindToDevice)
			return err
		}
	}

	return nil
}

type transportEndpointHeap []TransportEndpoint

var _ heap.Interface = (*transportEndpointHeap)(nil)

func (h *transportEndpointHeap) Len() int {
	return len(*h)
}

func (h *transportEndpointHeap) Less(i, j int) bool {
	return (*h)[i].UniqueID() < (*h)[j].UniqueID()
}

func (h *transportEndpointHeap) Swap(i, j int) {
	(*h)[i], (*h)[j] = (*h)[j], (*h)[i]
}

func (h *transportEndpointHeap) Push(x interface{}) {
	*h = append(*h, x.(TransportEndpoint))
}

func (h *transportEndpointHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return x
}

// multiPortEndpoint is a container for TransportEndpoints which are bound to
// the same pair of address and port. endpointsArr always has at least one
// element.
//
// FIXME(gvisor.dev/issue/873): Restore this properly. Currently, we just save
// this to ensure that the underlying endpoints get saved/restored, but not not
// use the restored copy.
//
// +stateify savable
type multiPortEndpoint struct {
	mu         sync.RWMutex `state:"nosave"`
	demux      *transportDemuxer
	netProto   tcpip.NetworkProtocolNumber
	transProto tcpip.TransportProtocolNumber

	endpoints transportEndpointHeap
	// reuse indicates if more than one endpoint is allowed.
	reuse bool
}

func (ep *multiPortEndpoint) transportEndpoints() []TransportEndpoint {
	ep.mu.RLock()
	eps := append([]TransportEndpoint(nil), ep.endpoints...)
	ep.mu.RUnlock()
	return eps
}

// reciprocalScale scales a value into range [0, n).
//
// This is similar to val % n, but faster.
// See http://lemire.me/blog/2016/06/27/a-fast-alternative-to-the-modulo-reduction/
func reciprocalScale(val, n uint32) uint32 {
	return uint32((uint64(val) * uint64(n)) >> 32)
}

// selectEndpoint calculates a hash of destination and source addresses and
// ports then uses it to select a socket. In this case, all packets from one
// address will be sent to same endpoint.
func selectEndpoint(id TransportEndpointID, mpep *multiPortEndpoint, seed uint32) TransportEndpoint {
	if len(mpep.endpoints) == 1 {
		return mpep.endpoints[0]
	}

	payload := []byte{
		byte(id.LocalPort),
		byte(id.LocalPort >> 8),
		byte(id.RemotePort),
		byte(id.RemotePort >> 8),
	}

	h := jenkins.Sum32(seed)
	h.Write(payload)
	h.Write([]byte(id.LocalAddress))
	h.Write([]byte(id.RemoteAddress))
	hash := h.Sum32()

	idx := reciprocalScale(hash, uint32(len(mpep.endpoints)))
	return mpep.endpoints[idx]
}

func (ep *multiPortEndpoint) handlePacketAll(r *Route, id TransportEndpointID, pkt PacketBuffer) {
	ep.mu.RLock()
	queuedProtocol, mustQueue := ep.demux.queuedProtocols[protocolIDs{ep.netProto, ep.transProto}]
	// HandlePacket takes ownership of pkt, so each endpoint needs
	// its own copy except for the final one.
	for _, endpoint := range ep.endpoints[:len(ep.endpoints)-1] {
		if mustQueue {
			queuedProtocol.QueuePacket(r, endpoint, id, pkt.Clone())
		} else {
			endpoint.HandlePacket(r, id, pkt.Clone())
		}
	}
	if endpoint := ep.endpoints[len(ep.endpoints)-1]; mustQueue {
		queuedProtocol.QueuePacket(r, endpoint, id, pkt)
	} else {
		endpoint.HandlePacket(r, id, pkt)
	}
	ep.mu.RUnlock() // Don't use defer for performance reasons.
}

// singleRegisterEndpoint tries to add an endpoint to the multiPortEndpoint
// list. The list might be empty already.
func (ep *multiPortEndpoint) singleRegisterEndpoint(t TransportEndpoint, reusePort bool) *tcpip.Error {
	ep.mu.Lock()
	defer ep.mu.Unlock()

	if len(ep.endpoints) != 0 {
		// If it was previously bound, we need to check if we can bind again.
		if !ep.reuse || !reusePort {
			return tcpip.ErrPortInUse
		}
	}

	heap.Push(&ep.endpoints, t)

	return nil
}

// unregisterEndpoint returns true if multiPortEndpoint has to be unregistered.
func (ep *multiPortEndpoint) unregisterEndpoint(t TransportEndpoint) bool {
	ep.mu.Lock()
	defer ep.mu.Unlock()

	for i, endpoint := range ep.endpoints {
		if endpoint == t {
			heap.Remove(&ep.endpoints, i)
			break
		}
	}
	return len(ep.endpoints) == 0
}

func (d *transportDemuxer) singleRegisterEndpoint(netProto tcpip.NetworkProtocolNumber, protocol tcpip.TransportProtocolNumber, id TransportEndpointID, ep TransportEndpoint, reusePort bool, bindToDevice tcpip.NICID) *tcpip.Error {
	if id.RemotePort != 0 {
		// TODO(eyalsoha): Why?
		reusePort = false
	}

	eps, ok := d.protocol[protocolIDs{netProto, protocol}]
	if !ok {
		return tcpip.ErrUnknownProtocol
	}

	eps.mu.Lock()
	defer eps.mu.Unlock()

	epsByNic, ok := eps.endpoints[id]
	if !ok {
		epsByNic = &endpointsByNic{
			endpoints: make(map[tcpip.NICID]*multiPortEndpoint),
			seed:      rand.Uint32(),
		}
		eps.endpoints[id] = epsByNic
	}

	return epsByNic.registerEndpoint(d, netProto, protocol, ep, reusePort, bindToDevice)
}

// unregisterEndpoint unregisters the endpoint with the given id such that it
// won't receive any more packets.
func (d *transportDemuxer) unregisterEndpoint(netProtos []tcpip.NetworkProtocolNumber, protocol tcpip.TransportProtocolNumber, id TransportEndpointID, ep TransportEndpoint, bindToDevice tcpip.NICID) {
	for _, n := range netProtos {
		if eps, ok := d.protocol[protocolIDs{n, protocol}]; ok {
			eps.unregisterEndpoint(id, ep, bindToDevice)
		}
	}
}

// deliverPacket attempts to find one or more matching transport endpoints, and
// then, if matches are found, delivers the packet to them. Returns true if
// the packet no longer needs to be handled.
func (d *transportDemuxer) deliverPacket(r *Route, protocol tcpip.TransportProtocolNumber, pkt PacketBuffer, id TransportEndpointID) bool {
	eps, ok := d.protocol[protocolIDs{r.NetProto, protocol}]
	if !ok {
		return false
	}

	// If the packet is a UDP broadcast or multicast, then find all matching
	// transport endpoints.
	if protocol == header.UDPProtocolNumber && isMulticastOrBroadcast(id.LocalAddress) {
		eps.mu.RLock()
		destEPs := d.findAllEndpointsLocked(eps, id)
		eps.mu.RUnlock()
		// Fail if we didn't find at least one matching transport endpoint.
		if len(destEPs) == 0 {
			r.Stats().UDP.UnknownPortErrors.Increment()
			return false
		}
		// handlePacket takes ownership of pkt, so each endpoint needs its own
		// copy except for the final one.
		for _, ep := range destEPs[:len(destEPs)-1] {
			ep.handlePacket(r, id, pkt.Clone())
		}
		destEPs[len(destEPs)-1].handlePacket(r, id, pkt)
		return true
	}

	// If the packet is a TCP packet with a non-unicast source or destination
	// address, then do nothing further and instruct the caller to do the same.
	if protocol == header.TCPProtocolNumber && (!isUnicast(r.LocalAddress) || !isUnicast(r.RemoteAddress)) {
		// TCP can only be used to communicate between a single source and a
		// single destination; the addresses must be unicast.
		r.Stats().TCP.InvalidSegmentsReceived.Increment()
		return true
	}

	eps.mu.RLock()
	ep := d.findEndpointLocked(eps, id)
	eps.mu.RUnlock()
	if ep == nil {
		if protocol == header.UDPProtocolNumber {
			r.Stats().UDP.UnknownPortErrors.Increment()
		}
		return false
	}
	ep.handlePacket(r, id, pkt)
	return true
}

// deliverRawPacket attempts to deliver the given packet and returns whether it
// was delivered successfully.
func (d *transportDemuxer) deliverRawPacket(r *Route, protocol tcpip.TransportProtocolNumber, pkt PacketBuffer) bool {
	eps, ok := d.protocol[protocolIDs{r.NetProto, protocol}]
	if !ok {
		return false
	}

	// As in net/ipv4/ip_input.c:ip_local_deliver, attempt to deliver via
	// raw endpoint first. If there are multiple raw endpoints, they all
	// receive the packet.
	foundRaw := false
	eps.mu.RLock()
	for _, rawEP := range eps.rawEndpoints {
		// Each endpoint gets its own copy of the packet for the sake
		// of save/restore.
		rawEP.HandlePacket(r, pkt)
		foundRaw = true
	}
	eps.mu.RUnlock()

	return foundRaw
}

// deliverControlPacket attempts to deliver the given control packet. Returns
// true if it found an endpoint, false otherwise.
func (d *transportDemuxer) deliverControlPacket(n *NIC, net tcpip.NetworkProtocolNumber, trans tcpip.TransportProtocolNumber, typ ControlType, extra uint32, pkt PacketBuffer, id TransportEndpointID) bool {
	eps, ok := d.protocol[protocolIDs{net, trans}]
	if !ok {
		return false
	}

	// Try to find the endpoint.
	eps.mu.RLock()
	ep := d.findEndpointLocked(eps, id)
	eps.mu.RUnlock()

	// Fail if we didn't find one.
	if ep == nil {
		return false
	}

	// Deliver the packet.
	ep.handleControlPacket(n, id, typ, extra, pkt)

	return true
}

// iterEndpointsLocked yields all endpointsByNic in eps that match id, in
// descending order of match quality. If a call to yield returns false,
// iterEndpointsLocked stops iteration and returns immediately.
//
// Preconditions: eps.mu must be locked.
func (d *transportDemuxer) iterEndpointsLocked(eps *transportEndpoints, id TransportEndpointID, yield func(*endpointsByNic) bool) {
	// Try to find a match with the id as provided.
	if ep, ok := eps.endpoints[id]; ok {
		if !yield(ep) {
			return
		}
	}

	// Try to find a match with the id minus the local address.
	nid := id

	nid.LocalAddress = ""
	if ep, ok := eps.endpoints[nid]; ok {
		if !yield(ep) {
			return
		}
	}

	// Try to find a match with the id minus the remote part.
	nid.LocalAddress = id.LocalAddress
	nid.RemoteAddress = ""
	nid.RemotePort = 0
	if ep, ok := eps.endpoints[nid]; ok {
		if !yield(ep) {
			return
		}
	}

	// Try to find a match with only the local port.
	nid.LocalAddress = ""
	if ep, ok := eps.endpoints[nid]; ok {
		if !yield(ep) {
			return
		}
	}
}

func (d *transportDemuxer) findAllEndpointsLocked(eps *transportEndpoints, id TransportEndpointID) []*endpointsByNic {
	var matchedEPs []*endpointsByNic
	d.iterEndpointsLocked(eps, id, func(ep *endpointsByNic) bool {
		matchedEPs = append(matchedEPs, ep)
		return true
	})
	return matchedEPs
}

// findTransportEndpoint find a single endpoint that most closely matches the provided id.
func (d *transportDemuxer) findTransportEndpoint(netProto tcpip.NetworkProtocolNumber, transProto tcpip.TransportProtocolNumber, id TransportEndpointID, r *Route) TransportEndpoint {
	eps, ok := d.protocol[protocolIDs{netProto, transProto}]
	if !ok {
		return nil
	}
	// Try to find the endpoint.
	eps.mu.RLock()
	epsByNic := d.findEndpointLocked(eps, id)
	// Fail if we didn't find one.
	if epsByNic == nil {
		eps.mu.RUnlock()
		return nil
	}

	epsByNic.mu.RLock()
	eps.mu.RUnlock()

	mpep, ok := epsByNic.endpoints[r.ref.nic.ID()]
	if !ok {
		if mpep, ok = epsByNic.endpoints[0]; !ok {
			epsByNic.mu.RUnlock() // Don't use defer for performance reasons.
			return nil
		}
	}

	ep := selectEndpoint(id, mpep, epsByNic.seed)
	epsByNic.mu.RUnlock()
	return ep
}

// findEndpointLocked returns the endpoint that most closely matches the given
// id.
func (d *transportDemuxer) findEndpointLocked(eps *transportEndpoints, id TransportEndpointID) *endpointsByNic {
	var matchedEP *endpointsByNic
	d.iterEndpointsLocked(eps, id, func(ep *endpointsByNic) bool {
		matchedEP = ep
		return false
	})
	return matchedEP
}

// registerRawEndpoint registers the given endpoint with the dispatcher such
// that packets of the appropriate protocol are delivered to it. A single
// packet can be sent to one or more raw endpoints along with a non-raw
// endpoint.
func (d *transportDemuxer) registerRawEndpoint(netProto tcpip.NetworkProtocolNumber, transProto tcpip.TransportProtocolNumber, ep RawTransportEndpoint) *tcpip.Error {
	eps, ok := d.protocol[protocolIDs{netProto, transProto}]
	if !ok {
		return tcpip.ErrNotSupported
	}

	eps.mu.Lock()
	eps.rawEndpoints = append(eps.rawEndpoints, ep)
	eps.mu.Unlock()

	return nil
}

// unregisterRawEndpoint unregisters the raw endpoint for the given transport
// protocol such that it won't receive any more packets.
func (d *transportDemuxer) unregisterRawEndpoint(netProto tcpip.NetworkProtocolNumber, transProto tcpip.TransportProtocolNumber, ep RawTransportEndpoint) {
	eps, ok := d.protocol[protocolIDs{netProto, transProto}]
	if !ok {
		panic(fmt.Errorf("tried to unregister endpoint with unsupported network and transport protocol pair: %d, %d", netProto, transProto))
	}

	eps.mu.Lock()
	for i, rawEP := range eps.rawEndpoints {
		if rawEP == ep {
			lastIdx := len(eps.rawEndpoints) - 1
			eps.rawEndpoints[i] = eps.rawEndpoints[lastIdx]
			eps.rawEndpoints[lastIdx] = nil
			eps.rawEndpoints = eps.rawEndpoints[:lastIdx]
			break
		}
	}
	eps.mu.Unlock()
}

func isMulticastOrBroadcast(addr tcpip.Address) bool {
	return addr == header.IPv4Broadcast || header.IsV4MulticastAddress(addr) || header.IsV6MulticastAddress(addr)
}

func isUnicast(addr tcpip.Address) bool {
	return addr != header.IPv4Any && addr != header.IPv6Any && !isMulticastOrBroadcast(addr)
}
