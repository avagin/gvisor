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

package kvm

import (
	"math/rand"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"gvisor.dev/gvisor/pkg/hostarch"
	"gvisor.dev/gvisor/pkg/ring0"
	"gvisor.dev/gvisor/pkg/ring0/pagetables"
	"gvisor.dev/gvisor/pkg/sentry/arch"
	"gvisor.dev/gvisor/pkg/sentry/arch/fpu"
	"gvisor.dev/gvisor/pkg/sentry/platform"
	"gvisor.dev/gvisor/pkg/sentry/platform/kvm/testutil"
	ktime "gvisor.dev/gvisor/pkg/sentry/time"
)

var dummyFPState = fpu.NewState()

type testHarness interface {
	Errorf(format string, args ...interface{})
	Fatalf(format string, args ...interface{})
}

func kvmTest(t testHarness, setup func(*KVM), fn func(*vCPU) bool) {
	// Create the machine.
	deviceFile, err := OpenDevice()
	if err != nil {
		t.Fatalf("error opening device file: %v", err)
	}
	k, err := New(deviceFile)
	if err != nil {
		t.Fatalf("error creating KVM instance: %v", err)
	}
	defer k.machine.Destroy()

	// Call additional setup.
	if setup != nil {
		setup(k)
	}

	var c *vCPU // For recovery.
	defer func() {
		redpill()
		if c != nil {
			k.machine.Put(c)
		}
	}()
	for {
		c = k.machine.Get()
		if !fn(c) {
			break
		}

		// We put the vCPU here and clear the value so that the
		// deferred recovery will not re-put it above.
		k.machine.Put(c)
		c = nil
	}
}

func bluepillTest(t testHarness, fn func(*vCPU)) {
	kvmTest(t, nil, func(c *vCPU) bool {
		bluepill(c)
		fn(c)
		return false
	})
}

func TestKernelSyscall(t *testing.T) {
	bluepillTest(t, func(c *vCPU) {
		redpill() // Leave guest mode.
		if got := atomic.LoadUint32(&c.state); got != vCPUUser {
			t.Errorf("vCPU not in ready state: got %v", got)
		}
	})
}

func hostFault() {
	defer func() {
		recover()
	}()
	var foo *int
	*foo = 0
}

func TestKernelFault(t *testing.T) {
	hostFault() // Ensure recovery works.
	bluepillTest(t, func(c *vCPU) {
		hostFault()
		if got := atomic.LoadUint32(&c.state); got != vCPUUser {
			t.Errorf("vCPU not in ready state: got %v", got)
		}
	})
}

func TestKernelFloatingPoint(t *testing.T) {
	bluepillTest(t, func(c *vCPU) {
		if !testutil.FloatingPointWorks() {
			t.Errorf("floating point does not work, and it should!")
		}
	})
}

func applicationTest(t testHarness, useHostMappings bool, target func(), fn func(*vCPU, *arch.Registers, *pagetables.PageTables) bool) {
	// Initialize registers & page tables.
	var (
		regs arch.Registers
		pt   *pagetables.PageTables
	)
	testutil.SetTestTarget(&regs, target)

	kvmTest(t, func(k *KVM) {
		// Create new page tables.
		as, _, err := k.NewAddressSpace(nil /* invalidator */)
		if err != nil {
			t.Fatalf("can't create new address space: %v", err)
		}
		pt = as.(*addressSpace).pageTables

		if useHostMappings {
			// Apply the physical mappings to these page tables.
			// (This is normally dangerous, since they point to
			// physical pages that may not exist. This shouldn't be
			// done for regular user code, but is fine for test
			// purposes.)
			applyPhysicalRegions(func(pr physicalRegion) bool {
				pt.Map(hostarch.Addr(pr.virtual), pr.length, pagetables.MapOpts{
					AccessType: hostarch.AnyAccess,
					User:       true,
				}, pr.physical)
				return true // Keep iterating.
			})
		}
	}, func(c *vCPU) bool {
		// Invoke the function with the extra data.
		return fn(c, &regs, pt)
	})
}

var userTestState = fpu.State{
	0x4b, 0x6f, 0x7, 0x8e, 0x24, 0x28, 0xe0, 0xff, 0x27, 0xd6, 0x7d, 0xa4,
	0x78, 0x47, 0xc6, 0xf3, 0x33, 0xfa, 0xe3, 0xe6, 0xb, 0x64, 0x7c, 0xf4,
	0x85, 0xf1, 0x0, 0x0, 0xfc, 0xe2, 0x57, 0xdc, 0x47, 0x7c, 0xea, 0xf8,
	0x10, 0x70, 0xee, 0x87, 0xde, 0x4a, 0xac, 0x1f, 0x95, 0x55, 0xee, 0x8d,
	0x1f, 0x16, 0xac, 0xf6, 0x1, 0x94, 0x3e, 0xb, 0x53, 0x2c, 0xec, 0x26,
	0xf2, 0x4d, 0x0, 0x92, 0xd9, 0x81, 0x1e, 0xc0, 0x8b, 0x6b, 0xe3, 0xf0,
	0x2d, 0xce, 0xce, 0x3f, 0xa0, 0x9d, 0xee, 0x99, 0x6b, 0xfe, 0x6e, 0x6a,
	0x5, 0xa9, 0xd3, 0xf6, 0x7d, 0x92, 0xb5, 0xe8, 0xa, 0xc7, 0x2, 0x92,
	0x2c, 0xbf, 0x5d, 0x81, 0x59, 0x5a, 0x1c, 0x23, 0xaa, 0x3e, 0x6e, 0x58,
	0x46, 0xea, 0xc2, 0xe0, 0x9a, 0x68, 0xa9, 0x7b, 0x4a, 0x72, 0xb5, 0xf5,
	0xab, 0x90, 0x8a, 0xe2, 0x97, 0xd0, 0x49, 0xef, 0x43, 0xf5, 0x4d, 0x19,
	0xdf, 0xb5, 0xa9, 0x0, 0xd2, 0x1a, 0xa9, 0x61, 0xac, 0xb7, 0x68, 0x38,
	0xc6, 0x40, 0x95, 0x1, 0xaf, 0xc7, 0x51, 0xe4, 0x8f, 0x51, 0x1c, 0x80,
	0x9c, 0x5e, 0x44, 0x5d, 0xf8, 0x6e, 0x72, 0x47, 0x45, 0xed, 0x90, 0x6f,
	0xab, 0xba, 0x78, 0x12, 0x61, 0x42, 0x43, 0x0, 0x44, 0x4c, 0xaf, 0x4b,
	0x8c, 0x64, 0xc4, 0xb4, 0x22, 0xcd, 0xbd, 0x1, 0x82, 0x53, 0x12, 0x58,
	0x76, 0xa9, 0x1e, 0xcb, 0xed, 0x0, 0xfe, 0x3c, 0x80, 0x58, 0x5d, 0xdb,
	0xe7, 0xb5, 0x90, 0x62, 0xbe, 0xee, 0x84, 0x2c, 0xc6, 0x98, 0xd5, 0xb7,
	0x2d, 0xa6, 0x54, 0xe3, 0x5c, 0x7c, 0x83, 0xd8, 0x36, 0x6e, 0xa7, 0xd8,
	0x1e, 0x3, 0x96, 0xc1, 0xf2, 0xa9, 0xe, 0x8d, 0xf4, 0x56, 0xdb, 0x2b,
	0xe3, 0xef, 0x4, 0xc3, 0x3e, 0x43, 0xf9, 0x64, 0xf1, 0xf8, 0x2f, 0x1d,
	0x4f, 0xe6, 0x6, 0x66, 0xb, 0xc4, 0x45, 0x5a, 0x5f, 0xd4, 0x1e, 0x38,
	0xfa, 0xee, 0x95, 0x8c, 0x10, 0xbe, 0xe8, 0xe7, 0xbd, 0xbd, 0xb9, 0x35,
	0xb7, 0x8f, 0x48, 0x52, 0x61, 0xe, 0xc2, 0x67, 0x57, 0x70, 0x5c, 0x6b,
	0x6c, 0xe9, 0xa7, 0xf8, 0x25, 0x1e, 0x26, 0x51, 0x53, 0x6c, 0xc2, 0xd3,
	0x36, 0x11, 0xfc, 0x9f, 0x8b, 0x67, 0xa1, 0xf3, 0xde, 0x84, 0xde, 0x8c,
	0x29, 0x22, 0x90, 0x5c, 0xd3, 0x0, 0xa6, 0x32, 0xab, 0xf1, 0x5a, 0xae,
	0x18, 0xf4, 0x61, 0xd5, 0xa3, 0x6c, 0xa9, 0x83, 0x3b, 0x49, 0xa1, 0x25,
	0xea, 0xc0, 0x81, 0x72, 0xf5, 0xd0, 0x88, 0x80, 0xc3, 0x17, 0x58, 0xc4,
	0x97, 0x4e, 0x16, 0xfc, 0x9a, 0x54, 0xe3, 0x4f, 0x3b, 0xbe, 0xb8, 0x34,
	0x8c, 0x8f, 0xa4, 0x34, 0xcd, 0x67, 0xc4, 0x83, 0xbe, 0xce, 0x18, 0x5b,
	0xf, 0x1b, 0xf6, 0x7f, 0x82, 0xa6, 0x83, 0xea, 0x90, 0xeb, 0xb6, 0xb8,
	0x86, 0x76, 0x2e, 0x17, 0x9f, 0xdb, 0xfc, 0xac, 0x98, 0x16, 0xa3, 0x6f,
	0x87, 0xbb, 0x56, 0x49, 0x81, 0x8a, 0x37, 0x98, 0xb3, 0x3a, 0x43, 0x69,
	0xed, 0xb6, 0xd9, 0xca, 0xaf, 0xc4, 0xa7, 0xfa, 0x85, 0x3f, 0x25, 0x91,
	0x6f, 0x9d, 0x3f, 0x15, 0x80, 0x52, 0xa, 0x44, 0x28, 0xa0, 0x77, 0xd1,
	0x1f, 0x9a, 0xd4, 0x3e, 0xdf, 0x54, 0x72, 0xeb, 0x9a, 0x54, 0x84, 0x39,
	0x19, 0xa, 0xa5, 0xa5, 0x88, 0x41, 0x13, 0x73, 0x6d, 0xa5, 0x25, 0x3b,
	0x7e, 0xd7, 0x5, 0xf3, 0x59, 0x7a, 0x8d, 0x38, 0x1c, 0x16, 0x8d, 0x23,
	0x55, 0x66, 0x52, 0x33, 0x0, 0xc5, 0x17, 0x3d, 0xd4, 0x9d, 0x2, 0x4,
	0x21, 0x61, 0xda, 0x90, 0x28, 0xec, 0x14, 0x0, 0x19, 0x5, 0x93, 0x7f,
	0xf9, 0x95, 0xb7, 0x32, 0x81, 0x1, 0x44, 0x96, 0xaf, 0xe1, 0x91, 0x42,
	0xab, 0x23, 0x30, 0xa7, 0xdf, 0x63, 0x44, 0x35, 0xe6, 0x0, 0x0, 0x0,
	0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0,
	0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0,
	0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0,
	0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0,
	0x0, 0x0, 0x0, 0x0, 0x2f, 0x77, 0xfb, 0xd2, 0xc1, 0x52, 0x5d, 0x85,
	0xd4, 0x59, 0xda, 0x82, 0x6b, 0xc5, 0xc8, 0xf6, 0x56, 0x80, 0xbc, 0xfa,
	0xca, 0xa7, 0xb9, 0x6d, 0x80, 0x2, 0x3, 0xb0, 0xa3, 0x3e, 0xf3, 0xec,
	0x94, 0xf1, 0x4f, 0xa5, 0x9b, 0xd5, 0xd0, 0xb0, 0x57, 0x40, 0xbc, 0xd0,
	0xc, 0x5d, 0x41, 0x49, 0xf6, 0x7e, 0x3b, 0xee, 0xb3, 0xb7, 0xb2, 0x3e,
	0x26, 0xa, 0x5c, 0xa0, 0x17, 0x25, 0x44, 0xb, 0x0, 0xa8, 0xc6, 0x29,
	0xcb, 0xfe, 0xa2, 0xd5, 0xca, 0x2f, 0xc6, 0x18, 0x40, 0x7f, 0x79, 0xd6,
	0xce, 0x32, 0x6d, 0x2f, 0xac, 0xde, 0xe7, 0x4f, 0xd8, 0x84, 0x3d, 0x95,
	0xbc, 0x87, 0x4d, 0x30, 0xca, 0x9f, 0x4b, 0xc3, 0x4, 0x97, 0x2b, 0x70,
	0x37, 0xbc, 0xf5, 0x7a, 0x67, 0x9d, 0x5d, 0x13, 0xf9, 0xf3, 0xa9, 0x2a,
	0x3b, 0x43, 0x94, 0xbd, 0xa8, 0x35, 0x3a, 0x15, 0xb, 0x2a, 0x33, 0xa3,
	0x90, 0x7d, 0x1b, 0x2e, 0x98, 0xfc, 0x77, 0x1d, 0x29, 0x9, 0xac, 0xca,
	0x23, 0xdc, 0xeb, 0x40, 0xe, 0x31, 0xbc, 0x5e, 0xe5, 0x22, 0x9a, 0xd0,
	0xcc, 0x8, 0xba, 0x9d, 0xc3, 0xa3, 0x69, 0xd1, 0x2e, 0x69, 0x2f, 0x6d,
	0xd9, 0xfe, 0x67, 0x24, 0x4d, 0xa9, 0x26, 0xc4, 0xf4, 0x3d, 0xd6, 0xc8,
	0x44, 0xb6, 0x62, 0x8a, 0x57, 0x7a, 0xd0, 0xa3, 0x52, 0x36, 0x85, 0x2f,
	0xde, 0x3d, 0xf3, 0xb0, 0xa1, 0xf3, 0x49, 0x89, 0x84, 0x40, 0x51, 0x3d,
	0xfc, 0x24, 0x35, 0x91, 0xe9, 0x22, 0x63, 0x88, 0x82, 0x95, 0xd2, 0xe7,
	0xec, 0xe8, 0xe5, 0x93, 0x26, 0x56, 0xae, 0x8f, 0xab, 0x6c, 0x64, 0xe9,
	0xeb, 0x6b, 0x9b, 0xec, 0x4c, 0x74, 0xa5, 0xf0, 0x89, 0xb7, 0x4c, 0xb3,
	0x2d, 0xd3, 0xf, 0xf7, 0xe6, 0xfd, 0x47, 0xef, 0x41, 0x3f, 0xbd, 0xd7,
	0xca, 0x17, 0x36, 0x2b, 0x4f, 0xb8, 0xe5, 0x80, 0xb, 0x48, 0xa8, 0x6,
	0xdd, 0x57, 0x52, 0x4b, 0xad, 0xeb, 0x71, 0x53, 0x69, 0x69, 0x7a, 0x2a,
	0xd4, 0x34, 0x3b, 0x39, 0x16, 0xba, 0xb7, 0x4e, 0x7e, 0x3, 0x77, 0x29,
	0x68, 0x71, 0x2c, 0xd4, 0xbb, 0x9b, 0xda, 0x1c, 0xcf, 0xe8, 0xe0, 0xb3,
	0x5c, 0x16, 0x8e, 0x14, 0x6e, 0x8b, 0x26, 0xb0, 0xaa, 0x4e, 0x27, 0x6,
	0x35, 0xe3, 0x27, 0x32, 0xbe, 0x19, 0xa8, 0x14, 0x40, 0x68, 0xc9, 0xae,
	0x32, 0x9d, 0x76, 0x60, 0xc, 0xd9, 0x9, 0x5, 0xaa, 0xe8, 0xde, 0x93,
	0xb2, 0x13, 0xc5, 0xcf, 0x73, 0xa1, 0x9c, 0xa5, 0xad, 0xaa, 0x8b, 0xcc,
	0xf7, 0x5a, 0x8b, 0x99, 0x18, 0xe6, 0xb1, 0x24, 0xc8, 0x29, 0xe1, 0x85,
	0x24, 0xd, 0xb3, 0x8a, 0xbc, 0x82, 0x8, 0x78, 0xde, 0x5d, 0xad, 0x37,
	0xf0, 0x4c, 0x12, 0x40, 0x93, 0x8d, 0x18, 0xee, 0x88, 0x8f, 0x80, 0x8c,
	0xc8, 0x7d, 0x9a, 0xf8, 0x68, 0xdc, 0x42, 0xe5, 0xe9, 0xf3, 0x77, 0x21,
	0x4c, 0x4d, 0x6a, 0x8, 0x7c, 0xcf, 0xae, 0xf2, 0x72, 0xa9, 0xd8, 0x2b,
	0x49, 0x8e, 0x7c, 0xf9, 0xc4, 0xad, 0xf3, 0x3c, 0xb6, 0xda, 0x95, 0x71,
	0xb7, 0x3c, 0x48, 0x5c, 0x3a, 0xe2, 0xcf, 0x32, 0xe7, 0x6f, 0x78, 0x42,
	0x59, 0x1a, 0xec, 0xea, 0x5b, 0x66, 0x2, 0xe3, 0xa8, 0x1c, 0x67, 0x55,
	0xda, 0x27, 0x74, 0xd5, 0x89, 0x9c, 0x38, 0x5b, 0x8, 0x6a, 0x48, 0xd2,
	0x28, 0x97, 0x90, 0x50, 0xf5, 0x5c, 0x7a, 0xae, 0xd0, 0x55, 0xe, 0x12,
	0x4a, 0x75, 0x32, 0x28, 0xdd, 0xf7, 0x2b, 0xeb, 0x91, 0x80, 0x5d, 0x4f,
	0x9c, 0x8e, 0xc8, 0xeb, 0xca, 0x96, 0x9, 0x40, 0x8c, 0x11, 0x23, 0x64,
	0x6a, 0x2c, 0x4a, 0x7, 0xa, 0x38, 0xa5, 0x80, 0xff, 0x79, 0x8f, 0xf0,
	0x53, 0x9f, 0xa7, 0xa6, 0xb5, 0xa1, 0xf4, 0x21, 0x50, 0x2f, 0xd7, 0x30,
	0x58, 0xc0, 0x8d, 0x6f, 0xdb, 0x56, 0x40, 0xd6, 0xdd, 0x5e, 0x97, 0x2f,
	0xc1, 0x7, 0xeb, 0x66, 0x81, 0x4f, 0xde, 0x57, 0xe7, 0x6f, 0x99, 0xfe,
	0x6f, 0xde, 0x92, 0x51, 0xb7, 0xae, 0x9f, 0x60, 0xb1, 0x2e, 0xbb, 0x5e,
	0x6b, 0x87, 0x13, 0x51, 0xfb, 0x8c, 0x5b, 0xd1, 0x35, 0xdc, 0xb3, 0x78,
	0xa4, 0xba, 0xe7, 0x51, 0x85, 0x3f, 0x1a, 0x3a, 0x24, 0x27, 0x8e, 0xd1,
	0xc0, 0xa7, 0x83, 0x9e, 0xcc, 0xd2, 0xf8, 0x91, 0xc, 0xdf, 0x8, 0xeb,
	0x85, 0x8d, 0xec, 0xde, 0x21, 0x8a, 0x5, 0x69, 0x7e, 0x1a, 0x78, 0x37,
	0xf1, 0x66, 0xde, 0xe5, 0xe9, 0x9, 0xfb, 0x6b, 0xe2, 0xf9, 0xf8, 0xf4,
	0x46, 0x6e, 0x74, 0x5d, 0xf8, 0xb, 0x30, 0x5f, 0x26, 0xd1, 0xdd, 0xf7,
	0x5f, 0xe1, 0xdd, 0x8, 0xa4, 0x84, 0x24, 0xac, 0x2d, 0xdd, 0x7b, 0x34,
	0x8c, 0xbf, 0x32, 0xac, 0xb7, 0xd2, 0x4a, 0x41, 0x29, 0x40, 0xf1, 0xae,
	0x15, 0x63, 0x3d, 0x6f, 0x2f, 0x45, 0x60, 0x8c, 0xea, 0x9, 0xc7, 0x90,
	0x76, 0xa3, 0xca, 0xf6, 0x9c, 0x9b, 0x31, 0x9e, 0x2c, 0xa9, 0xed, 0x81,
	0x2e, 0xc0, 0xdb, 0xae, 0x9a, 0x39, 0xd2, 0xa4, 0x29, 0xec, 0x63, 0x47,
	0x28, 0x8a, 0xc5, 0x34, 0xbb, 0x2f, 0xb0, 0x64, 0xa3, 0x24, 0x6b, 0xf5,
	0x3a, 0x21, 0x31, 0xa7, 0xb7, 0x24, 0x5, 0x34, 0xca, 0xa6, 0x4a, 0xb7,
	0x8a, 0xa6, 0x29, 0x6, 0x7b, 0xd3, 0x8c, 0x9f, 0x11, 0xd0, 0xa1, 0xdc,
	0x73, 0xe6, 0xaf, 0x31, 0x4f, 0xb2, 0x95, 0x83, 0x15, 0xbc, 0x54, 0x38,
	0x86, 0x30, 0x71, 0x86, 0xd8, 0x3, 0xf9, 0x9f, 0x8f, 0x65, 0xa3, 0xca,
	0x25, 0x55, 0x14, 0x55, 0xf5, 0x3d, 0x4b, 0x80, 0x2c, 0x87, 0xce, 0xd6,
	0x1d, 0x87, 0xf5, 0xef, 0x63, 0x26, 0xe2, 0xf0, 0x6d, 0x25, 0xc9, 0xe,
	0xab, 0x73, 0xa, 0xd3, 0xea, 0x2f, 0xe6, 0x68, 0xee, 0xba, 0xc4, 0xf5,
	0x4f, 0xdc, 0x65, 0xe8, 0x5e, 0x26, 0xf8, 0x1c, 0xe6, 0xbb, 0x4a, 0xe4,
	0xea, 0xac, 0xf9, 0x92, 0xd, 0x63, 0x8e, 0x75, 0x6d, 0xb4, 0xb7, 0x8c,
	0x5a, 0xca, 0xd9, 0x63, 0x98, 0x38, 0xac, 0x96, 0x23, 0xa5, 0x6f, 0x7b,
	0x73, 0x1a, 0x37, 0x22, 0xf6, 0x84, 0x15, 0xa7, 0x46, 0x51, 0x1a, 0x3a,
	0xa8, 0xaa, 0x1c, 0x6, 0x3d, 0x99, 0xc6, 0x9e, 0xe4, 0x3f, 0xa7, 0x34,
	0xc6, 0x2b, 0x30, 0xa4, 0xf1, 0x70, 0xda, 0xbb, 0xa4, 0xcf, 0x9e, 0x69,
	0x9b, 0x13, 0xc2, 0x3a, 0xd1, 0xc9, 0xb6, 0xd3, 0x3, 0xbb, 0xb9, 0x3c,
	0x97, 0x6, 0xa, 0x11, 0xb2, 0x26, 0x20, 0x55, 0x8f, 0x44, 0xb2, 0x8c,
	0xd0, 0x5b, 0x5d, 0x65, 0xd5, 0x5c, 0x23, 0xcc, 0x29, 0x82, 0xa7, 0x8a,
	0x18, 0xaf, 0x59, 0xae, 0x63, 0x9d, 0xfb, 0x45, 0x68, 0x8b, 0xb6, 0x98,
	0xa3, 0x7f, 0xa0, 0xf3, 0x94, 0xd, 0x90, 0xd7, 0x4a, 0x1b, 0xd3, 0xa1,
	0x37, 0xdd, 0x83, 0xf4, 0x90, 0x50, 0x47, 0x26, 0x92, 0x3d, 0x95, 0xfb,
	0x89, 0xd8, 0x59, 0xa0, 0xf5, 0xb3, 0xb7, 0xf9, 0x2e, 0xed, 0x4, 0xd7,
	0xd0, 0xa5, 0xa1, 0xdc, 0x86, 0xa, 0x3c, 0xa0, 0x94, 0xf3, 0x90, 0x4e,
	0x4f, 0x37, 0xde, 0x80, 0x4e, 0xb5, 0xb9, 0x5b, 0xa1, 0xb4, 0x24, 0xde,
	0x1b, 0x6d, 0x54, 0x79, 0x8d, 0x85, 0xa9, 0x8f, 0x2, 0xee, 0xac, 0xee,
	0x2a, 0xc0, 0x9d, 0xe3, 0x92, 0x77, 0xde, 0x3b, 0x3, 0x9d, 0xe7, 0xbd,
	0x94, 0x94, 0xc2, 0x15, 0x2f, 0x92, 0x5d, 0x6a, 0x59, 0xd, 0xdf, 0x17,
	0xe, 0x92, 0x97, 0x7e, 0x77, 0x8d, 0xde, 0xa1, 0x85, 0xf0, 0x38, 0xee,
	0x55, 0x86, 0xf5, 0x6c, 0x49, 0x68, 0x90, 0xed, 0x59, 0xa2, 0x4f, 0xd9,
	0x3, 0xe9, 0x7a, 0x1, 0x5a, 0xc1, 0xf8, 0x85, 0x12, 0x65, 0xe3, 0x2f,
	0xd1, 0x32, 0x59, 0xc9, 0x60, 0xe5, 0x67, 0x22, 0x28, 0x97, 0x5e, 0x65,
	0x39, 0xec, 0x13, 0x8e, 0x85, 0x56, 0xf, 0x9f, 0x1d, 0xc4, 0xb1, 0xee,
	0x85, 0x96, 0x82, 0x1, 0x87, 0x2f, 0x8e, 0x72, 0x7c, 0x7a, 0xfd, 0xef,
	0xee, 0x87, 0x11, 0x5f, 0xcb, 0x7b, 0xc6, 0x1, 0x2, 0x8c, 0xf3, 0xb6,
	0x5a, 0xc4, 0x24, 0x3d, 0x16, 0x80, 0xf4, 0xc, 0x9, 0x3b, 0x6, 0x2,
	0xb8, 0xff, 0xc5, 0xa2, 0xb9, 0x6a, 0xb5, 0x85, 0x66, 0xf2, 0xb2, 0x26,
	0x3f, 0xe2, 0x19, 0xc5, 0x17, 0x73, 0x2f, 0xfb, 0xd3, 0x6e, 0xd1, 0xe6,
	0x45, 0xe5, 0x91, 0x2a, 0xeb, 0x7c, 0x0, 0xf7, 0x2a, 0x28, 0x83, 0x9d,
	0x90, 0x11, 0xa0, 0x94, 0x31, 0x9b, 0xeb, 0x7a, 0x43, 0x82, 0x6d, 0x8c,
	0x60, 0x91, 0x7e, 0x7, 0xc5, 0x29, 0x43, 0x21, 0x6a, 0x62, 0xac, 0x14,
	0x14, 0x74, 0xbc, 0xd4, 0xfb, 0xe4, 0xe1, 0x13, 0x12, 0x2b, 0x0, 0x55,
	0x3b, 0xbe, 0x6f, 0x31, 0x22, 0x1c, 0x4, 0x5a, 0x53, 0x8b, 0xd0, 0x26,
	0x32, 0xaf, 0x48, 0xb5, 0xff, 0xd9, 0xed, 0x94, 0x48, 0xba, 0x7b, 0x4f,
	0xc7, 0x88, 0xe2, 0x53, 0x6, 0x9a, 0x29, 0x1b, 0x89, 0x75, 0xb7, 0x7a,
	0x2b, 0xfb, 0x1d, 0x35, 0x14, 0xa7, 0x25, 0xf6, 0x3d, 0xae, 0x8b, 0x8b,
	0xe5, 0x82, 0x76, 0x1f, 0x99, 0x8d, 0xe8, 0xd7, 0xbc, 0xc4, 0x95, 0xd7,
	0x8b, 0x7f, 0x15, 0xdf, 0x2d, 0x7e, 0xab, 0x84, 0xc0, 0xf8, 0x75, 0xf8,
	0xfe, 0xb1, 0x31, 0xd7, 0x6c, 0x7e, 0x23, 0x39, 0xad, 0x5d, 0x6c, 0x54,
	0xb6, 0xdc, 0xb9, 0x7, 0x7b, 0xf5, 0x4c, 0xfd, 0x20, 0xd1, 0x3, 0xf1,
	0x5a, 0x8f, 0x1f, 0x47, 0x74, 0x21, 0x9e, 0x56, 0xba, 0xd9, 0x89, 0x34,
	0x1a, 0xa8, 0x2c, 0xa, 0x4a, 0x73, 0x71, 0x67, 0xae, 0x30, 0x35, 0x90,
	0xa5, 0x1c, 0xc, 0xba, 0xda, 0xbd, 0xe9, 0x78, 0x63, 0xc9, 0x1d, 0x36,
	0x31, 0xb, 0x25, 0x9c, 0xaa, 0x7e, 0x5b, 0x1e, 0x50, 0x3, 0xda, 0x11,
	0xed, 0x65, 0x19, 0x31, 0xc9, 0x73, 0x4e, 0xe8, 0xc, 0x62, 0xf3, 0x8e,
	0x61, 0x12, 0xf5, 0x21, 0x6f, 0xbe, 0xa0, 0x24, 0xd5, 0x34, 0xc8, 0xd2,
	0xb3, 0x1c, 0xb2, 0xa3, 0x2f, 0xd0, 0xb2, 0x83, 0x1f, 0x60, 0xe, 0xea,
	0x62, 0xc5, 0x1b, 0x56, 0x66, 0xa5, 0x57, 0x19, 0xc, 0x6f, 0x19, 0x19,
	0x7b, 0x62, 0x1, 0x21, 0x93, 0xd8, 0x11, 0x23, 0x89, 0xae, 0xef, 0x28,
	0xc8, 0x9b, 0x5a, 0x22, 0xaa, 0x30, 0x1, 0x5d, 0x99, 0x49, 0xb6, 0x3,
	0xf5, 0xfe, 0xdc, 0x1e, 0xb6, 0x97, 0x63, 0x21, 0xc2, 0xab, 0xce, 0xf0,
	0x51, 0xe4, 0x99, 0xe7, 0xb3, 0x74, 0xd, 0x6d, 0x49, 0x8a, 0xa3, 0xad,
	0xf8, 0xc0, 0x11, 0x58, 0x2e, 0xfa, 0xe, 0xc7, 0x57, 0x39, 0xf4, 0x16,
	0x3f, 0x72, 0xbf, 0x67, 0xc6, 0xda, 0xdd, 0x20, 0x2f, 0x4b, 0x96, 0xb3,
	0x59, 0x11, 0x8c, 0x27, 0x64, 0x2c, 0x66, 0xa8, 0x0, 0x57, 0xc5, 0xad,
	0xcb, 0xe8, 0xba, 0xe9, 0x99, 0xfb, 0xbf, 0x97, 0x51, 0x63, 0x53, 0x39,
	0x25, 0xd9, 0x95, 0x91, 0x64, 0xe7, 0x24, 0x16, 0x62, 0x89, 0xc2, 0xb4,
	0x1d, 0xbd, 0x6d, 0x2c, 0xd6, 0x42, 0xb1, 0x4e, 0x81, 0x36, 0xc, 0xcf,
	0x5c, 0x34, 0xfc, 0xb4, 0x3a, 0xf0, 0x3a, 0x5c, 0xe6, 0x42, 0xe8, 0x4b,
	0x7b, 0xbc, 0x59, 0x11, 0x59, 0x64, 0x4f, 0xad, 0x86, 0xe5, 0x2e, 0x78,
	0x7d, 0x29, 0xc4, 0x14, 0x10, 0x76, 0x91, 0x19, 0x78, 0x9f, 0xa4, 0xc6,
	0xe4, 0xfd, 0x56, 0x6e, 0x54, 0x81, 0xfe, 0xa4, 0x31, 0x39, 0xd1, 0x49,
	0x58, 0xc3, 0x1c, 0xde, 0x3a, 0xb6, 0xcd, 0x8d, 0xca, 0xb4, 0x30, 0x15,
	0xfb, 0x2b, 0x1b, 0xcd, 0x43, 0xbf, 0xb0, 0x59, 0xda, 0xbb, 0x85, 0xb2,
	0x14, 0x87, 0x13, 0x6, 0x5a, 0x3e, 0x7e, 0x84, 0xc8, 0x84, 0x9d, 0x5b,
	0x6f, 0x93, 0x18, 0x8a, 0x2d, 0x88, 0xa7, 0x38, 0xa4, 0x85, 0x85, 0xb3,
	0x21, 0x54, 0xe, 0x5e, 0xd, 0xfd, 0xf1, 0x5, 0xd2, 0x8a, 0x49, 0xac,
	0xa8, 0xa8, 0xae, 0x52, 0x86, 0xf3, 0x1f, 0x87, 0x22, 0x23, 0x27, 0x25,
	0xb3, 0xcd, 0x29, 0xfa, 0x31, 0xb5, 0x4c, 0xd6, 0x64, 0x4a, 0x9b, 0x29,
	0x31, 0xd, 0x71, 0x38, 0x73, 0x58, 0x4e, 0x63, 0x71, 0x58, 0x31, 0x64,
	0xbe, 0xf7, 0x93, 0xb1, 0xbb, 0x5b, 0x48, 0x1d, 0x2, 0xf6, 0x64, 0x5c,
	0xe2, 0x8c, 0x5e, 0x24, 0x89, 0x6d, 0xdd, 0xc0, 0x5b, 0x22, 0x64, 0xba,
	0x7, 0x11, 0x53, 0x2a, 0xf4, 0xf7, 0xaf, 0xef, 0xf0, 0x8, 0x8c, 0x62,
	0x74, 0xc6, 0x37, 0x80, 0xe5, 0x73, 0x9a, 0x53, 0x87, 0xd4, 0xe5, 0x20,
	0x16, 0x41, 0xe7, 0x89, 0xe8, 0x46, 0x79, 0x92, 0x35, 0x9, 0x1f, 0xdc,
	0xd4, 0xf5, 0xfc, 0x10, 0x54, 0x4b, 0xf0, 0x4f, 0xda, 0x64, 0xb0, 0xd5,
	0xe7, 0xbb, 0xe6, 0xe7, 0x5b, 0x35, 0x4d, 0x4f, 0xb3, 0x1e, 0xb3, 0x46,
	0x92, 0x1a, 0xb1, 0xcc, 0x9b, 0xb2, 0x7d, 0x4, 0x90, 0xe5, 0xae, 0xd2,
	0x5d, 0xc9, 0xf6, 0x92, 0x5, 0x22, 0x49, 0x99, 0x1f, 0xb7, 0x4, 0x81,
	0xe, 0x64, 0xde, 0x4e, 0x3f, 0xe6, 0x27, 0x53, 0xc5, 0xbd, 0x51, 0x9f,
	0xc8, 0xc1, 0xbc, 0xf5, 0xd0, 0xee, 0xc2, 0x6d, 0x60, 0xda, 0x3c, 0xc2,
	0xdd, 0x7e, 0x11, 0xa5, 0xbf, 0x93, 0x56, 0xfc, 0xbf, 0x38, 0x91, 0x54,
	0xf6, 0x3a, 0x77, 0x61, 0xd9, 0x87, 0x3b, 0x69, 0x37, 0xed, 0x5a, 0xe6,
	0x30, 0x14, 0xd0, 0xe5, 0xa4, 0x49, 0xc5, 0xd7, 0x75, 0x2c, 0xb2, 0x55,
	0x47, 0x41, 0x16, 0x47, 0x5f, 0xac, 0x89, 0x98, 0xd2, 0x3b, 0x16, 0x22,
	0x79, 0x64, 0xd1, 0x56, 0x3, 0x81, 0x2e, 0x5b, 0x10, 0x18, 0x61, 0x2f,
	0x74, 0x68, 0xb5, 0xee, 0x8, 0x66, 0x46, 0x6a, 0x35, 0x8a, 0xa6, 0xad,
	0x93, 0x43, 0x16, 0x3b, 0x49, 0x5e, 0x5b, 0x28, 0xda, 0x84, 0x73, 0x45,
	0x9, 0xd9, 0xc, 0x68, 0x31, 0x19, 0x7e, 0x83, 0x65, 0x53, 0x2b, 0x7b,
	0xdc, 0x30, 0x36, 0x9e, 0x62, 0x3f, 0xde, 0xff, 0x88, 0x61, 0xb0, 0x2a,
	0x47, 0xd2, 0xf1, 0x40, 0x59, 0x26, 0x15, 0x82, 0xb4, 0xd3, 0x14, 0x9d,
	0x1b, 0xd0, 0xb, 0x7, 0xb0, 0xed, 0x7e, 0xb5, 0xd2, 0x5d, 0x18, 0xbc,
	0xab, 0xe5, 0xbf, 0xfd, 0x94, 0xa6, 0xe9, 0xd7, 0x81, 0xcb, 0xb1, 0x6d,
	0x6e, 0xa4, 0xd4, 0xac, 0xe6, 0x20, 0x2c, 0x95, 0x68, 0x8, 0x29, 0x8,
	0xa2, 0xe4, 0x8b, 0x2e, 0xc3, 0x35, 0x34, 0x52, 0x1f, 0x21, 0x43, 0x1d,
	0x9d, 0x43, 0xe0, 0xbb, 0x52, 0xe4, 0x97, 0xee, 0x74, 0x6a, 0xe7, 0xa6,
	0x3f, 0x85, 0x98, 0x6f, 0x88, 0x4f, 0xa6, 0x98, 0x3d, 0xc8, 0xe5, 0x17,
	0x5a, 0x9b, 0xca, 0x98, 0xf2, 0x43, 0xf4, 0x81, 0xa2, 0xef, 0x4c, 0x63,
	0xb6, 0xac, 0x64, 0xfc, 0xbe, 0xf4, 0x7d, 0x2b, 0x38, 0xe9, 0xdc, 0x7a,
	0xbf, 0x6d, 0x59, 0xd7, 0xfd, 0xa2, 0x47, 0x7c, 0xe1, 0xec, 0x3c, 0x5b,
	0xfd, 0x9c, 0xf, 0x16, 0x4e, 0x46, 0xe9, 0xa3, 0xd0, 0x39, 0xe8, 0xac,
	0x2a, 0x68, 0x43, 0xb, 0x44, 0xed, 0xa7, 0x56, 0x66, 0xa8, 0x4b, 0x91,
	0xc3, 0x4d, 0x53, 0x71, 0xdd, 0x69, 0x6b, 0x49, 0x5f, 0xd9, 0x1a, 0xeb,
	0xef, 0xdf, 0xb5, 0x19, 0xa7, 0x55, 0xf1, 0x7b, 0x34, 0x9c, 0xe5, 0xd8,
	0x80, 0x9d, 0xb3, 0x85, 0x48, 0x56, 0xeb, 0xea, 0x65, 0x83, 0x62, 0xdc,
	0x8d, 0x3f, 0x30, 0x3b, 0xc0, 0xfc, 0x9a, 0x26, 0xcf, 0xeb, 0xfe, 0x6a,
	0xe0, 0x63, 0xe0, 0x16, 0x3a, 0xc8, 0x66, 0xd8, 0x2f, 0xdc, 0xd3, 0x7a,
	0x2e, 0x83, 0xc3, 0xec, 0x22, 0x94, 0xef, 0x34, 0xdf, 0xe7, 0x90, 0xa8,
	0x98, 0xb4, 0xcf, 0xaf, 0x91, 0x1d, 0xe8, 0xbe, 0x63, 0x71, 0x1c, 0xf0,
	0xde, 0x43, 0xc2, 0x2d, 0xad, 0x1d, 0xd6, 0x21, 0x6a, 0xec, 0x54, 0x6f,
	0x9f, 0x98, 0xb, 0xe2, 0xe, 0x8, 0xfd, 0xf1, 0x6f, 0xce, 0xed, 0x1c,
	0xde, 0xc3, 0x32, 0xf6,
}

func TestApplicationFPU(t *testing.T) {
	state := fpu.NewState()
	copy(state, userTestState)
	applicationTest(t, true, testutil.SpinLoop, func(c *vCPU, regs *arch.Registers, pt *pagetables.PageTables) bool {
		go func() {
			time.Sleep(time.Millisecond)
			c.BounceToKernel()
		}()
		var si arch.SignalInfo
		if _, err := c.SwitchToUser(ring0.SwitchOpts{
			Registers:          regs,
			FloatingPointState: &state,
			PageTables:         pt,
		}, &si); err != platform.ErrContextInterrupt {
			t.Errorf("application partial restore: got %v, wanted %v", err, platform.ErrContextInterrupt)
		}
		return false
	})
}

func TestApplicationSyscall(t *testing.T) {
	applicationTest(t, true, testutil.SyscallLoop, func(c *vCPU, regs *arch.Registers, pt *pagetables.PageTables) bool {
		var si arch.SignalInfo
		if _, err := c.SwitchToUser(ring0.SwitchOpts{
			Registers:          regs,
			FloatingPointState: &dummyFPState,
			PageTables:         pt,
			FullRestore:        true,
		}, &si); err == platform.ErrContextInterrupt {
			return true // Retry.
		} else if err != nil {
			t.Errorf("application syscall with full restore failed: %v", err)
		}
		return false
	})
	applicationTest(t, true, testutil.SyscallLoop, func(c *vCPU, regs *arch.Registers, pt *pagetables.PageTables) bool {
		var si arch.SignalInfo
		if _, err := c.SwitchToUser(ring0.SwitchOpts{
			Registers:          regs,
			FloatingPointState: &dummyFPState,
			PageTables:         pt,
		}, &si); err == platform.ErrContextInterrupt {
			return true // Retry.
		} else if err != nil {
			t.Errorf("application syscall with partial restore failed: %v", err)
		}
		return false
	})
}

func TestApplicationFault(t *testing.T) {
	applicationTest(t, true, testutil.Touch, func(c *vCPU, regs *arch.Registers, pt *pagetables.PageTables) bool {
		testutil.SetTouchTarget(regs, nil) // Cause fault.
		var si arch.SignalInfo
		if _, err := c.SwitchToUser(ring0.SwitchOpts{
			Registers:          regs,
			FloatingPointState: &dummyFPState,
			PageTables:         pt,
			FullRestore:        true,
		}, &si); err == platform.ErrContextInterrupt {
			return true // Retry.
		} else if err != platform.ErrContextSignal || si.Signo != int32(unix.SIGSEGV) {
			t.Errorf("application fault with full restore got (%v, %v), expected (%v, SIGSEGV)", err, si, platform.ErrContextSignal)
		}
		return false
	})
	applicationTest(t, true, testutil.Touch, func(c *vCPU, regs *arch.Registers, pt *pagetables.PageTables) bool {
		testutil.SetTouchTarget(regs, nil) // Cause fault.
		var si arch.SignalInfo
		if _, err := c.SwitchToUser(ring0.SwitchOpts{
			Registers:          regs,
			FloatingPointState: &dummyFPState,
			PageTables:         pt,
		}, &si); err == platform.ErrContextInterrupt {
			return true // Retry.
		} else if err != platform.ErrContextSignal || si.Signo != int32(unix.SIGSEGV) {
			t.Errorf("application fault with partial restore got (%v, %v), expected (%v, SIGSEGV)", err, si, platform.ErrContextSignal)
		}
		return false
	})
}

func TestRegistersSyscall(t *testing.T) {
	applicationTest(t, true, testutil.TwiddleRegsSyscall, func(c *vCPU, regs *arch.Registers, pt *pagetables.PageTables) bool {
		testutil.SetTestRegs(regs) // Fill values for all registers.
		for {
			var si arch.SignalInfo
			if _, err := c.SwitchToUser(ring0.SwitchOpts{
				Registers:          regs,
				FloatingPointState: &dummyFPState,
				PageTables:         pt,
			}, &si); err == platform.ErrContextInterrupt {
				continue // Retry.
			} else if err != nil {
				t.Errorf("application register check with partial restore got unexpected error: %v", err)
			}
			if err := testutil.CheckTestRegs(regs, false); err != nil {
				t.Errorf("application register check with partial restore failed: %v", err)
			}
			break // Done.
		}
		return false
	})
}

func TestRegistersFault(t *testing.T) {
	applicationTest(t, true, testutil.TwiddleRegsFault, func(c *vCPU, regs *arch.Registers, pt *pagetables.PageTables) bool {
		testutil.SetTestRegs(regs) // Fill values for all registers.
		for {
			var si arch.SignalInfo
			if _, err := c.SwitchToUser(ring0.SwitchOpts{
				Registers:          regs,
				FloatingPointState: &dummyFPState,
				PageTables:         pt,
				FullRestore:        true,
			}, &si); err == platform.ErrContextInterrupt {
				continue // Retry.
			} else if err != platform.ErrContextSignal || si.Signo != int32(unix.SIGSEGV) {
				t.Errorf("application register check with full restore got unexpected error: %v", err)
			}
			if err := testutil.CheckTestRegs(regs, true); err != nil {
				t.Errorf("application register check with full restore failed: %v", err)
			}
			break // Done.
		}
		return false
	})
}

func TestBounce(t *testing.T) {
	applicationTest(t, true, testutil.SpinLoop, func(c *vCPU, regs *arch.Registers, pt *pagetables.PageTables) bool {
		go func() {
			time.Sleep(time.Millisecond)
			c.BounceToKernel()
		}()
		var si arch.SignalInfo
		if _, err := c.SwitchToUser(ring0.SwitchOpts{
			Registers:          regs,
			FloatingPointState: &dummyFPState,
			PageTables:         pt,
		}, &si); err != platform.ErrContextInterrupt {
			t.Errorf("application partial restore: got %v, wanted %v", err, platform.ErrContextInterrupt)
		}
		return false
	})
	applicationTest(t, true, testutil.SpinLoop, func(c *vCPU, regs *arch.Registers, pt *pagetables.PageTables) bool {
		go func() {
			time.Sleep(time.Millisecond)
			c.BounceToKernel()
		}()
		var si arch.SignalInfo
		if _, err := c.SwitchToUser(ring0.SwitchOpts{
			Registers:          regs,
			FloatingPointState: &dummyFPState,
			PageTables:         pt,
			FullRestore:        true,
		}, &si); err != platform.ErrContextInterrupt {
			t.Errorf("application full restore: got %v, wanted %v", err, platform.ErrContextInterrupt)
		}
		return false
	})
}

func TestBounceStress(t *testing.T) {
	applicationTest(t, true, testutil.SpinLoop, func(c *vCPU, regs *arch.Registers, pt *pagetables.PageTables) bool {
		randomSleep := func() {
			// O(hundreds of microseconds) is appropriate to ensure
			// different overlaps and different schedules.
			if n := rand.Intn(1000); n > 100 {
				time.Sleep(time.Duration(n) * time.Microsecond)
			}
		}
		for i := 0; i < 1000; i++ {
			// Start an asynchronously executing goroutine that
			// calls Bounce at pseudo-random point in time.
			// This should wind up calling Bounce when the
			// kernel is in various stages of the switch.
			go func() {
				randomSleep()
				c.BounceToKernel()
			}()
			randomSleep()
			var si arch.SignalInfo
			if _, err := c.SwitchToUser(ring0.SwitchOpts{
				Registers:          regs,
				FloatingPointState: &dummyFPState,
				PageTables:         pt,
			}, &si); err != platform.ErrContextInterrupt {
				t.Errorf("application partial restore: got %v, wanted %v", err, platform.ErrContextInterrupt)
			}
			c.unlock()
			randomSleep()
			c.lock()
		}
		return false
	})
}

func TestInvalidate(t *testing.T) {
	var data uintptr // Used below.
	applicationTest(t, true, testutil.Touch, func(c *vCPU, regs *arch.Registers, pt *pagetables.PageTables) bool {
		testutil.SetTouchTarget(regs, &data) // Read legitimate value.
		for {
			var si arch.SignalInfo
			if _, err := c.SwitchToUser(ring0.SwitchOpts{
				Registers:          regs,
				FloatingPointState: &dummyFPState,
				PageTables:         pt,
			}, &si); err == platform.ErrContextInterrupt {
				continue // Retry.
			} else if err != nil {
				t.Errorf("application partial restore: got %v, wanted nil", err)
			}
			break // Done.
		}
		// Unmap the page containing data & invalidate.
		pt.Unmap(hostarch.Addr(reflect.ValueOf(&data).Pointer() & ^uintptr(hostarch.PageSize-1)), hostarch.PageSize)
		for {
			var si arch.SignalInfo
			if _, err := c.SwitchToUser(ring0.SwitchOpts{
				Registers:          regs,
				FloatingPointState: &dummyFPState,
				PageTables:         pt,
				Flush:              true,
			}, &si); err == platform.ErrContextInterrupt {
				continue // Retry.
			} else if err != platform.ErrContextSignal {
				t.Errorf("application partial restore: got %v, wanted %v", err, platform.ErrContextSignal)
			}
			break // Success.
		}
		return false
	})
}

// IsFault returns true iff the given signal represents a fault.
func IsFault(err error, si *arch.SignalInfo) bool {
	return err == platform.ErrContextSignal && si.Signo == int32(unix.SIGSEGV)
}

func TestEmptyAddressSpace(t *testing.T) {
	applicationTest(t, false, testutil.SyscallLoop, func(c *vCPU, regs *arch.Registers, pt *pagetables.PageTables) bool {
		var si arch.SignalInfo
		if _, err := c.SwitchToUser(ring0.SwitchOpts{
			Registers:          regs,
			FloatingPointState: &dummyFPState,
			PageTables:         pt,
		}, &si); err == platform.ErrContextInterrupt {
			return true // Retry.
		} else if !IsFault(err, &si) {
			t.Errorf("first fault with partial restore failed got %v", err)
			t.Logf("registers: %#v", &regs)
		}
		return false
	})
	applicationTest(t, false, testutil.SyscallLoop, func(c *vCPU, regs *arch.Registers, pt *pagetables.PageTables) bool {
		var si arch.SignalInfo
		if _, err := c.SwitchToUser(ring0.SwitchOpts{
			Registers:          regs,
			FloatingPointState: &dummyFPState,
			PageTables:         pt,
			FullRestore:        true,
		}, &si); err == platform.ErrContextInterrupt {
			return true // Retry.
		} else if !IsFault(err, &si) {
			t.Errorf("first fault with full restore failed got %v", err)
			t.Logf("registers: %#v", &regs)
		}
		return false
	})
}

func TestWrongVCPU(t *testing.T) {
	kvmTest(t, nil, func(c1 *vCPU) bool {
		kvmTest(t, nil, func(c2 *vCPU) bool {
			// Basic test, one then the other.
			bluepill(c1)
			bluepill(c2)
			if c1.guestExits == 0 {
				// Check: vCPU1 will exit due to redpill() in bluepill(c2).
				// Don't allow the test to proceed if this fails.
				t.Fatalf("wrong vCPU#1 exits: vCPU1=%+v,vCPU2=%+v", c1, c2)
			}

			// Alternate vCPUs; we expect to need to trigger the
			// wrong vCPU path on each switch.
			for i := 0; i < 100; i++ {
				bluepill(c1)
				bluepill(c2)
			}
			if count := c1.guestExits; count < 90 {
				t.Errorf("wrong vCPU#1 exits: vCPU1=%+v,vCPU2=%+v", c1, c2)
			}
			if count := c2.guestExits; count < 90 {
				t.Errorf("wrong vCPU#2 exits: vCPU1=%+v,vCPU2=%+v", c1, c2)
			}
			return false
		})
		return false
	})
	kvmTest(t, nil, func(c1 *vCPU) bool {
		kvmTest(t, nil, func(c2 *vCPU) bool {
			bluepill(c1)
			bluepill(c2)
			return false
		})
		return false
	})
}

func TestRdtsc(t *testing.T) {
	var i int // Iteration count.
	kvmTest(t, nil, func(c *vCPU) bool {
		start := ktime.Rdtsc()
		bluepill(c)
		guest := ktime.Rdtsc()
		redpill()
		end := ktime.Rdtsc()
		if start > guest || guest > end {
			t.Errorf("inconsistent time: start=%d, guest=%d, end=%d", start, guest, end)
		}
		i++
		return i < 100
	})
}

func BenchmarkApplicationSyscall(b *testing.B) {
	var (
		i int // Iteration includes machine.Get() / machine.Put().
		a int // Count for ErrContextInterrupt.
	)
	applicationTest(b, true, testutil.SyscallLoop, func(c *vCPU, regs *arch.Registers, pt *pagetables.PageTables) bool {
		var si arch.SignalInfo
		if _, err := c.SwitchToUser(ring0.SwitchOpts{
			Registers:          regs,
			FloatingPointState: &dummyFPState,
			PageTables:         pt,
		}, &si); err == platform.ErrContextInterrupt {
			a++
			return true // Ignore.
		} else if err != nil {
			b.Fatalf("benchmark failed: %v", err)
		}
		i++
		return i < b.N
	})
	if a != 0 {
		b.Logf("ErrContextInterrupt occurred %d times (in %d iterations).", a, a+i)
	}
}

func BenchmarkKernelSyscall(b *testing.B) {
	// Note that the target passed here is irrelevant, we never execute SwitchToUser.
	applicationTest(b, true, testutil.Getpid, func(c *vCPU, regs *arch.Registers, pt *pagetables.PageTables) bool {
		// iteration does not include machine.Get() / machine.Put().
		for i := 0; i < b.N; i++ {
			testutil.Getpid()
		}
		return false
	})
}

func BenchmarkWorldSwitchToUserRoundtrip(b *testing.B) {
	// see BenchmarkApplicationSyscall.
	var (
		i int
		a int
	)
	applicationTest(b, true, testutil.SyscallLoop, func(c *vCPU, regs *arch.Registers, pt *pagetables.PageTables) bool {
		var si arch.SignalInfo
		if _, err := c.SwitchToUser(ring0.SwitchOpts{
			Registers:          regs,
			FloatingPointState: &dummyFPState,
			PageTables:         pt,
		}, &si); err == platform.ErrContextInterrupt {
			a++
			return true // Ignore.
		} else if err != nil {
			b.Fatalf("benchmark failed: %v", err)
		}
		// This will intentionally cause the world switch. By executing
		// a host syscall here, we force the transition between guest
		// and host mode.
		testutil.Getpid()
		i++
		return i < b.N
	})
	if a != 0 {
		b.Logf("ErrContextInterrupt occurred %d times (in %d iterations).", a, a+i)
	}
}
