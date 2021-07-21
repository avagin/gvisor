package atomic

import (
	aatomic "sync/atomic"
)

// Value is a required type parameter.
type Value struct{}

// Atomic is a type that implements atomic operations.
//
// +stateify savable
type Atomic struct {
	val Value `state:".(*Value)"`
}

//go:inline
func (a *Atomic) Load() Value {
	return aatomic.LoadValueOp(&a.val)
}

//go:inline
func (a *Atomic) Store(v Value) {
	aatomic.StoreValueOp(&a.val, v)
}

//go:inline
func (a *Atomic) Swap(v Value) Value {
	return aatomic.SwapValueOp(&a.val, v)
}
