package components

import "sync"

// LatestDispatcher keeps at most one UI callback queued and renders its newest value.
type LatestDispatcher[T any] struct {
	dispatch func(func())
	render   func(T)

	mu      sync.Mutex
	latest  T
	pending bool
}

func NewLatestDispatcher[T any](dispatch func(func()), render func(T)) *LatestDispatcher[T] {
	return &LatestDispatcher[T]{dispatch: dispatch, render: render}
}

func (d *LatestDispatcher[T]) Submit(value T) {
	d.mu.Lock()
	d.latest = value
	if d.pending {
		d.mu.Unlock()
		return
	}
	d.pending = true
	d.mu.Unlock()
	d.dispatch(func() {
		d.mu.Lock()
		latest := d.latest
		d.pending = false
		d.mu.Unlock()
		d.render(latest)
	})
}
