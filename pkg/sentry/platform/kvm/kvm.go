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

// Package kvm provides a kvm-based implementation of the platform interface.
package kvm

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
	"gvisor.dev/gvisor/pkg/abi/linux"
	"gvisor.dev/gvisor/pkg/hostarch"
	"gvisor.dev/gvisor/pkg/ring0"
	"gvisor.dev/gvisor/pkg/ring0/pagetables"
	"gvisor.dev/gvisor/pkg/safecopy"
	"gvisor.dev/gvisor/pkg/seccomp"
	"gvisor.dev/gvisor/pkg/sentry/platform"
	"gvisor.dev/gvisor/pkg/sync"
)

// userMemoryRegion is a region of physical memory.
//
// This mirrors kvm_memory_region.
type userMemoryRegion struct {
	slot          uint32
	flags         uint32
	guestPhysAddr uint64
	memorySize    uint64
	userspaceAddr uint64
}

// runData is the run structure. This may be mapped for synchronous register
// access (although that doesn't appear to be supported by my kernel at least).
//
// This mirrors kvm_run.
type runData struct {
	requestInterruptWindow uint8
	_                      [7]uint8

	exitReason                 uint32
	readyForInterruptInjection uint8
	ifFlag                     uint8
	_                          [2]uint8

	cr8      uint64
	apicBase uint64

	// This is the union data for exits. Interpretation depends entirely on
	// the exitReason above (see vCPU code for more information).
	data [32]uint64
}

// KVM represents a lightweight VM context.
type KVM struct {
	platform.NoCPUPreemptionDetection

	// KVM never changes mm_structs.
	platform.UseHostProcessMemoryBarrier

	// machine is the backing VM.
	machine *machine
}

var (
	globalOnce sync.Once
	globalErr  error
)

// OpenDevice opens the KVM device at /dev/kvm and returns the File.
func OpenDevice() (*os.File, error) {
	f, err := os.OpenFile("/dev/kvm", unix.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("error opening /dev/kvm: %v", err)
	}
	return f, nil
}

var seccompMachine *machine

// archMmapHandler creates a new memory region and maps it to the guest.
//
//go:nosplit
func seccompMmapHandler(context unsafe.Pointer) {

	addr, length, errno := seccompMmapSyscall(context)
	if errno != 0 {
		return
	}

	m := seccompMachine
	if m == nil {
		return
	}

	// Map the new region to the guest.
	vr := region{
		virtual: addr,
		length:  length,
	}
	for virtual := vr.virtual; virtual < vr.virtual+vr.length; {
		physical, length, ok := translateToPhysical(virtual)
		if !ok {
			// This must be an invalid region that was
			// knocked out by creation of the physical map.
			return
		}
		if virtual+length > vr.virtual+vr.length {
			// Cap the length to the end of the area.
			length = vr.virtual + vr.length - virtual
		}

		// Ensure the physical range is mapped.
		m.mapPhysical(physical, length, physicalRegions, _KVM_MEM_FLAGS_NONE)
		virtual += length
	}
}

func seccompMmapRules(m *machine) {
	if seccompMachine != nil {
		panic("seccompMachine is already set")
	}
	seccompMachine = m

	// Install the handler.
	if err := safecopy.ReplaceSignalHandler(unix.SIGSYS, addrOfSigsysHandler(), &savedSigsysHandler); err != nil {
		panic(fmt.Sprintf("Unable to set handler for signal %d: %v", bluepillSignal, err))
	}
	rules := []seccomp.RuleSet{}
	rules = append(rules, []seccomp.RuleSet{
		// Trap mmap system calls and handle them in sigsysGoHandler
		{
			Rules: seccomp.SyscallRules{
				unix.SYS_MMAP: {
					{
						seccomp.MatchAny{},
						seccomp.MatchAny{},
						seccomp.MatchAny{},
						/* MAP_DENYWRITE is ignored and used only for filtering. */
						seccomp.MaskedEqual(unix.MAP_DENYWRITE, 0),
					},
				},
			},
			Action: linux.SECCOMP_RET_TRAP,
		},
	}...)
	instrs, err := seccomp.BuildProgram(rules, linux.SECCOMP_RET_ALLOW, linux.SECCOMP_RET_ALLOW)
	if err != nil {
		panic(fmt.Sprintf("failed to build rules: %v", err))
	}
	// Perform the actual installation.
	if err := seccomp.SetFilter(instrs); err != nil {
		panic(fmt.Sprintf("failed to set filter: %v", err))
	}

}

// New returns a new KVM-based implementation of the platform interface.
func New(deviceFile *os.File) (*KVM, error) {
	fd := deviceFile.Fd()

	// Ensure global initialization is done.
	globalOnce.Do(func() {
		globalErr = updateGlobalOnce(int(fd))
	})
	if globalErr != nil {
		return nil, globalErr
	}

	// Create a new VM fd.
	var (
		vm    uintptr
		errno unix.Errno
	)
	for {
		vm, _, errno = unix.Syscall(unix.SYS_IOCTL, fd, _KVM_CREATE_VM, 0)
		if errno == unix.EINTR {
			continue
		}
		if errno != 0 {
			return nil, fmt.Errorf("creating VM: %v", errno)
		}
		break
	}
	// We are done with the device file.
	deviceFile.Close()

	// Create a VM context.
	machine, err := newMachine(int(vm))
	if err != nil {
		return nil, err
	}

	// All set.
	return &KVM{
		machine: machine,
	}, nil
}

// SupportsAddressSpaceIO implements platform.Platform.SupportsAddressSpaceIO.
func (*KVM) SupportsAddressSpaceIO() bool {
	return false
}

// CooperativelySchedulesAddressSpace implements platform.Platform.CooperativelySchedulesAddressSpace.
func (*KVM) CooperativelySchedulesAddressSpace() bool {
	return false
}

// MapUnit implements platform.Platform.MapUnit.
func (*KVM) MapUnit() uint64 {
	// We greedily creates PTEs in MapFile, so extremely large mappings can
	// be expensive. Not _that_ expensive since we allow super pages, but
	// even though can get out of hand if you're creating multi-terabyte
	// mappings. For this reason, we limit mappings to an arbitrary 16MB.
	return 16 << 20
}

// MinUserAddress returns the lowest available address.
func (*KVM) MinUserAddress() hostarch.Addr {
	return hostarch.PageSize
}

// MaxUserAddress returns the first address that may not be used.
func (*KVM) MaxUserAddress() hostarch.Addr {
	return hostarch.Addr(ring0.MaximumUserAddress)
}

// NewAddressSpace returns a new pagetable root.
func (k *KVM) NewAddressSpace(_ interface{}) (platform.AddressSpace, <-chan struct{}, error) {
	// Allocate page tables and install system mappings.
	pageTables := pagetables.NewWithUpper(newAllocator(), k.machine.upperSharedPageTables, ring0.KernelStartAddress)

	// Return the new address space.
	return &addressSpace{
		machine:    k.machine,
		pageTables: pageTables,
		dirtySet:   k.machine.newDirtySet(),
	}, nil, nil
}

// NewContext returns an interruptible context.
func (k *KVM) NewContext() platform.Context {
	return &context{
		machine: k.machine,
	}
}

type constructor struct{}

func (*constructor) New(f *os.File) (platform.Platform, error) {
	return New(f)
}

func (*constructor) OpenDevice() (*os.File, error) {
	return OpenDevice()
}

// Flags implements platform.Constructor.Flags().
func (*constructor) Requirements() platform.Requirements {
	return platform.Requirements{}
}

func init() {
	platform.Register("kvm", &constructor{})
}
