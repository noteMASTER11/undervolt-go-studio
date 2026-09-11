package components

import "testing"

func TestLatestDispatcherCoalescesPendingUpdates(t *testing.T) {
	var queued []func()
	var rendered []int
	dispatcher := NewLatestDispatcher(func(callback func()) {
		queued = append(queued, callback)
	}, func(value int) {
		rendered = append(rendered, value)
	})
	dispatcher.Submit(1)
	dispatcher.Submit(2)
	dispatcher.Submit(3)
	if len(queued) != 1 {
		t.Fatalf("queued callbacks = %d", len(queued))
	}
	queued[0]()
	if len(rendered) != 1 || rendered[0] != 3 {
		t.Fatalf("rendered = %v", rendered)
	}
}
