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

func TestLatestDispatcherCancelDropsQueuedRenderUntilResumed(t *testing.T) {
	var queued []func()
	var rendered []int
	dispatcher := NewLatestDispatcher(func(callback func()) { queued = append(queued, callback) }, func(value int) {
		rendered = append(rendered, value)
	})
	dispatcher.Submit(1)
	dispatcher.Cancel()
	queued[0]()
	if len(rendered) != 0 {
		t.Fatalf("rendered after cancel = %v", rendered)
	}
	dispatcher.Resume()
	dispatcher.Submit(2)
	if len(queued) != 2 {
		t.Fatalf("queued callbacks = %d, want 2", len(queued))
	}
	queued[1]()
	if len(rendered) != 1 || rendered[0] != 2 {
		t.Fatalf("rendered after resume = %v", rendered)
	}
}
