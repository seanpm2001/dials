//go:build go1.19

package dials

import (
	"sync/atomic"
)

// Dials is the main access point for your configuration.
type Dials[T any] struct {
	value       atomic.Pointer[T]
	updatesChan chan *T
	params      Params[T]
	cbch        chan<- userCallbackEvent
}

// View returns the configuration struct populated.
func (d *Dials[T]) View() *T {
	return d.value.Load()
}
