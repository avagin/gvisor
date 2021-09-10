// Copyright 2019 The gVisor Authors.
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
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	// reservedMemory is a chunk of physical memory reserved starting at
	// physical address zero. There are some special pages in this region,
	// so we just call the whole thing off.
	reservedMemory = 0x100000000
)

// archMmapHandler creates a new memory region and maps it to the guest.
//
//go:nosplit
func seccompMmapHandler(context unsafe.Pointer) {
	ctx := bluepillArchContext(context)

	if ctx.Rdi != 0 {
		throw("unexpected mmap adddress")
	}

	addr, _, e := unix.RawSyscall6(uintptr(ctx.Rax), 0xfffffffffffff000, uintptr(ctx.Rsi), uintptr(ctx.Rdx), uintptr(ctx.R10), uintptr(ctx.R8), uintptr(ctx.R9))
	ctx.Rax = uint64(addr)
	if e != 0 {
		return
	}

	m := seccompMachine
	if m == nil {
		return
	}

	// Map the new region to the guest.
	vr := region{
		virtual: addr,
		length:  uintptr(ctx.Rsi),
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
