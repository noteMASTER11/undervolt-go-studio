package components

import "sync"

// LatestDispatcher keeps at most one UI callback queued and renders its newest value.
type LatestDispatcher[T any] struct {
	dispatch func(func())
	render   func(T)

	mu      sync.Mutex
	latest  T
	pending bool
	active  bool
	epoch   uint64
}

func NewLatestDispatcher[T any](dispatch func(func()), render func(T)) *LatestDispatcher[T] {
	return &LatestDispatcher[T]{dispatch: dispatch, render: render, active: true}
}

func (d *LatestDispatcher[T]) Submit(value T) {
	d.mu.Lock()
	if !d.active {
		d.mu.Unlock()
		return
	}
	d.latest = value
	if d.pending {
		d.mu.Unlock()
		return
	}
	d.pending = true
	epoch := d.epoch
	d.mu.Unlock()
	d.dispatch(func() {
		d.mu.Lock()
		if !d.active || d.epoch != epoch {
			d.mu.Unlock()
			return
		}
		latest := d.latest
		d.pending = false
		d.mu.Unlock()
		d.render(latest)
	})
}

func (d *LatestDispatcher[T]) Cancel() {
	d.mu.Lock()
	d.active = false
	d.pending = false
	d.epoch++
	d.mu.Unlock()
}

func (d *LatestDispatcher[T]) Resume() {
	d.mu.Lock()
	d.active = true
	d.mu.Unlock()
}
