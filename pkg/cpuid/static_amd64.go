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
func (fs FeatureSet) ToStatic() Static {
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

	// Processor Extended State Enumeration.
	for i := uint32(0); i < xSaveInfoNumLeaves; i++ {
		in := In{Eax: uint32(xSaveInfo), Ecx: i}
		s[in] = fs.Query(in)
	}

	// Save all features (may be redundant).
	for feature := range allFeatures {
		feature.set(s, fs.HasFeature(feature))
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
	for in, out := range s {
		log.Debugf("ToStatic: CPUID(%#v) -> %#v", in, out)
	}

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
	log.Warningf("CPUID: static: %#v -> %#v", in, out)
	return out
}
