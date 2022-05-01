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

//go:build amd64
// +build amd64

package cpuid

import (
	"gvisor.dev/gvisor/pkg/log"
)

// Static is a static CPUID function.
//
// +stateify savable
type Static map[In]Out

// Fixed converts the FeatureSet to a fixed set.
func (fs FeatureSet) Fixed() FeatureSet {
	return fs.ToStatic().ToFeatureSet()
}

// ToStatic converts a FeatureSet to a Static function.
//
// You can create a new static feature set as:
//
//	fs := otherFeatureSet.ToStatic().ToFeatureSet()
func (fs FeatureSet) ToStatic1() Static {
	s := make(Static)

	// Save all allowed top-level functions.
	for fn, allowed := range allowedBasicFunctions {
		if allowed {
			in := In{Eax: uint32(fn)}
			s[in] = fs.Query(in)
		}
	}

	in := In{Eax: 0x80000000}
	out := fs.Query(in)
	extMax := int(out.Eax)
	log.Debugf("The highest vale of extended CPUID: %x", extMax)
	// Save all allowed extended functions.
	for fn, allowed := range allowedExtendedFunctions {
		if allowed && fn <= extMax {
			in := In{Eax: uint32(fn) + uint32(extendedStart)}
			n := uint32(1)
			if cpuidFunction(in.Eax) == cacheProperties {
				n = 4 // Data Cache, Instruction Cache, L2 and L3.
			}
			for i := uint32(0); i < n; i++ {
				in.Ecx = i
				s[in] = fs.Query(in)
				log.Debugf("CPUID(%#v) -> %#v", in, s[in])
			}
		}
	}

	// Save all features (may be redundant).
	for feature := range allFeatures {
		feature.set(s, fs.HasFeature(feature))
	}

	// Processor Extended State Enumeration.
	for i := uint32(0); i < xSaveInfoNumLeaves; i++ {
		in := In{Eax: uint32(xSaveInfo), Ecx: i}
		s[in] = fs.Query(in)
	}

	// Save all cache information.
	out = fs.Query(In{Eax: uint32(featureInfo)})
	for i := uint32(0); i < out.Ecx; i++ {
		in := In{Eax: uint32(intelDeterministicCacheParams), Ecx: i}
		out := fs.Query(in)
		s[in] = out
		if CacheType(out.Eax&0xf) == cacheNull {
			break
		}
	}

	return s
}

func (fs FeatureSet) ToStatic() Static	{
	s := make(Static)
	s[In{Eax:0x0}] = Out{Eax:0x10, Ebx:0x68747541, Ecx:0x444d4163, Edx:0x69746e65}
	s[In{Eax:0x1}] = Out{Eax:0xa20f10, Ebx:0xb200800, Ecx:0x7ed8320b, Edx:0x178bfbff}
	s[In{Eax:0x7, Ecx:0x0}] = Out{Eax:0x0, Ebx:0x219c97a9, Ecx:0x40069c, Edx:0x10}
	s[In{Eax:0x7, Ecx:0x1}] = Out{Eax:0x0, Ebx:0x0, Ecx:0x0, Edx:0x0}
	s[In{Eax:0x80000000}] = Out{Eax:0x80000023, Ebx:0x68747541, Ecx:0x444d4163, Edx:0x69746e65}
	s[In{Eax:0x80000001}] = Out{Eax:0xa20f10, Ebx:0x20000000, Ecx:0x75c237ff, Edx:0x2fd3fbff}
	s[In{Eax:0x80000002}] = Out{Eax:0x20444d41, Ebx:0x657a7952, Ecx:0x2039206e, Edx:0x30353935}
	s[In{Eax:0x80000003}] = Out{Eax:0x36312058, Ebx:0x726f432d, Ecx:0x72502065, Edx:0x7365636f}
	s[In{Eax:0x80000004}] = Out{Eax:0x20726f73, Ebx:0x20202020, Ecx:0x20202020, Edx:0x202020}
	s[In{Eax:0x80000005}] = Out{Eax:0xff40ff40, Ebx:0xff40ff40, Ecx:0x20080140, Edx:0x20080140}
	s[In{Eax:0x80000006}] = Out{Eax:0x48002200, Ebx:0x68004200, Ecx:0x2006140, Edx:0x2009140}
	s[In{Eax:0x80000008}] = Out{Eax:0x3030, Ebx:0x111ef657, Ecx:0x501f, Edx:0x10000}
	s[In{Eax:0x8000001a, Ecx:0x0}] = Out{Eax:0x6, Ebx:0x0, Ecx:0x0, Edx:0x0}
	s[In{Eax:0x8000001d, Ecx:0x0}] = Out{Eax:0x4121, Ebx:0x1c0003f, Ecx:0x3f, Edx:0x0}
	s[In{Eax:0x8000001d, Ecx:0x1}] = Out{Eax:0x4122, Ebx:0x1c0003f, Ecx:0x3f, Edx:0x0}
	s[In{Eax:0x8000001d, Ecx:0x2}] = Out{Eax:0x4143, Ebx:0x1c0003f, Ecx:0x3ff, Edx:0x2}
	s[In{Eax:0x8000001d, Ecx:0x3}] = Out{Eax:0x3c163, Ebx:0x3c0003f, Ecx:0x7fff, Edx:0x1}
	s[In{Eax:0x8000001e, Ecx:0x0}] = Out{Eax:0xb, Ebx:0x105, Ecx:0x0, Edx:0x0}
	s[In{Eax:0xd, Ecx:0x0}] = Out{Eax:0x207, Ebx:0x988, Ecx:0x988, Edx:0x0}
	s[In{Eax:0xd, Ecx:0x1}] = Out{Eax:0xf, Ebx:0x348, Ecx:0x1800, Edx:0x0}
	s[In{Eax:0xd, Ecx:0x2}] = Out{Eax:0x100, Ebx:0x240, Ecx:0x0, Edx:0x0}
	s[In{Eax:0xd, Ecx:0x3}] = Out{Eax:0x0, Ebx:0x0, Ecx:0x0, Edx:0x0}
	s[In{Eax:0xd, Ecx:0x5}] = Out{Eax:0x0, Ebx:0x0, Ecx:0x0, Edx:0x0}
	s[In{Eax:0xd, Ecx:0x6}] = Out{Eax:0x0, Ebx:0x0, Ecx:0x0, Edx:0x0}
	s[In{Eax:0xd, Ecx:0x7}] = Out{Eax:0x0, Ebx:0x0, Ecx:0x0, Edx:0x0}
	s = s.Remove(X86FeatureAVX2)
	log.Debugf("========= %s", s.ToFeatureSet().FlagString())
	return s
}

// ToFeatureSet converts a static specification to a FeatureSet.
//
// This overloads some local values, where required.
func (s Static) ToFeatureSet() FeatureSet {
	// Make a copy.
	ns := make(Static)
	for k, v := range s {
		ns[k] = v
	}
	ns.normalize()
	return FeatureSet{ns}
}

// afterLoad calls normalize.
func (s Static) afterLoad() {
	s.normalize()
}

// normalize normalizes FPU sizes.
func (s Static) normalize() {
	// Override local FPU sizes, which must be fixed.
	fs := FeatureSet{s}
	if fs.HasFeature(X86FeatureXSAVE) {
		in := In{Eax: uint32(xSaveInfo)}
		out := s[in]
		out.Ecx = maxXsaveSize
		s[in] = out
	}
}

// Add adds a feature.
func (s Static) Add(feature Feature) Static {
	feature.set(s, true)
	return s
}

// Remove removes a feature.
func (s Static) Remove(feature Feature) Static {
	feature.set(s, false)
	return s
}

// Set implements ChangeableSet.Set.
func (s Static) Set(in In, out Out) {
	s[in] = out
}

// Query implements Function.Query.
func (s Static) Query(in In) Out {
	in.normalize()
	out, ok := s[in]
	if !ok {
		log.Warningf("Unknown CPUID(%#v)", in)
	}
	return out
}
