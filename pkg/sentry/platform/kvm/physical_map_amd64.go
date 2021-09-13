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

//go:nosplit
func seccompMmapSyscall(context unsafe.Pointer) (uintptr, uintptr, unix.Errno) {
	ctx := bluepillArchContext(context)

	// MAP_DENYWRITE is deprecated and ignored by kernel. We use it only for seccomp filters.
	addr, _, e := unix.RawSyscall6(uintptr(ctx.Rax), uintptr(ctx.Rdi), uintptr(ctx.Rsi),
		uintptr(ctx.Rdx), uintptr(ctx.R10)|unix.MAP_DENYWRITE, uintptr(ctx.R8), uintptr(ctx.R9))
	ctx.Rax = uint64(addr)

	return addr, uintptr(ctx.Rsi), e
}
