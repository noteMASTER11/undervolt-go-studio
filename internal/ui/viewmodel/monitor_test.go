package viewmodel

import (
	"sync"
	"testing"
	"time"

	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
)

type fakeHandle struct {
	frames  chan telemetry.Frame
	once    sync.Once
	onClose func()
}

func newFakeHandle(onClose func()) *fakeHandle {
	return &fakeHandle{frames: make(chan telemetry.Frame, 1), onClose: onClose}
}

func (h *fakeHandle) Frames() <-chan telemetry.Frame { return h.frames }
func (h *fakeHandle) Close() {
	h.once.Do(func() {
		if h.onClose != nil {
			h.onClose()
		}
		close(h.frames)
	})
}

type fakeSource struct {
	mu             sync.Mutex
	subscribeCalls int
	closeCalls     int
	subscriptions  []telemetry.Subscription
	handles        []*fakeHandle
}

func (s *fakeSource) Subscribe(subscription telemetry.Subscription) SubscriptionHandle {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subscribeCalls++
	s.subscriptions = append(s.subscriptions, subscription)
	handle := newFakeHandle(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.closeCalls++
	})
	s.handles = append(s.handles, handle)
	return handle
}

func TestMonitorSubscribesOnlyWhileActive(t *testing.T) {
	source := &fakeSource{}
	vm := NewMonitor(source, 250*time.Millisecond)
	if source.subscribeCalls != 0 {
		t.Fatal("eager subscription")
	}
	vm.Activate()
	if source.subscribeCalls != 1 {
		t.Fatalf("subscribe calls = %d", source.subscribeCalls)
	}
	vm.Deactivate()
	if source.closeCalls != 1 {
		t.Fatalf("close calls = %d", source.closeCalls)
	}
}

func TestMonitorIntervalClampsAndRestartsActiveSubscription(t *testing.T) {
	source := &fakeSource{}
	vm := NewMonitor(source, 250*time.Millisecond)
	vm.SetInterval(10 * time.Millisecond)
	if vm.Interval() != 100*time.Millisecond {
		t.Fatalf("low = %s", vm.Interval())
	}
	vm.SetInterval(9 * time.Second)
	if vm.Interval() != 5*time.Second {
		t.Fatalf("high = %s", vm.Interval())
	}

	vm.Activate()
	vm.SetInterval(time.Second)
	vm.Deactivate()
	if source.subscribeCalls != 2 || source.closeCalls != 2 {
		t.Fatalf("subscriptions=%d closes=%d", source.subscribeCalls, source.closeCalls)
	}
}

func TestMonitorPublishesCopiedStateFromFrames(t *testing.T) {
	source := &fakeSource{}
	vm := NewMonitor(source, 250*time.Millisecond)
	vm.SetMetricIDs([]telemetry.MetricID{"cpu.utilization"})
	states := make(chan MonitorState, 2)
	vm.SetListener(func(state MonitorState) { states <- state })
	vm.Activate()
	t.Cleanup(vm.Deactivate)

	source.mu.Lock()
	handle := source.handles[0]
	source.mu.Unlock()
	handle.frames <- telemetry.Frame{Samples: []telemetry.Sample{{
		MetricID: "cpu.utilization", Value: 42, Quality: telemetry.QualityGood, Timestamp: time.Now(),
	}}}

	select {
	case state := <-states:
		if got := state.Current["cpu.utilization"].Value; got != 42 {
			t.Fatalf("current value = %v", got)
		}
		state.Current["cpu.utilization"] = telemetry.Sample{Value: 99}
		if got := vm.State().Current["cpu.utilization"].Value; got != 42 {
			t.Fatalf("state alias changed stored value to %v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for state")
	}
}

func TestMonitorStateExcludesDeselectedHistory(t *testing.T) {
	source := &fakeSource{}
	vm := NewMonitor(source, 250*time.Millisecond)
	vm.SetMetricIDs([]telemetry.MetricID{"a", "b"})
	vm.current["a"] = telemetry.Sample{MetricID: "a", Value: 1}
	vm.current["b"] = telemetry.Sample{MetricID: "b", Value: 2}
	vm.histories["a"].Append(vm.current["a"])
	vm.histories["b"].Append(vm.current["b"])
	vm.SetMetricIDs([]telemetry.MetricID{"b"})
	state := vm.State()
	if _, exists := state.Current["a"]; exists {
		t.Fatal("deselected current value was copied")
	}
	if _, exists := state.History["a"]; exists {
		t.Fatal("deselected history was copied")
	}
}

func TestMonitorDoesNotPlotUnavailableAsZero(t *testing.T) {
	source := &fakeSource{}
	vm := NewMonitor(source, 250*time.Millisecond)
	vm.SetMetricIDs([]telemetry.MetricID{"temperature"})
	states := make(chan MonitorState, 2)
	vm.SetListener(func(state MonitorState) { states <- state })
	vm.Activate()
	defer vm.Deactivate()
	source.handles[0].frames <- telemetry.Frame{Samples: []telemetry.Sample{{MetricID: "temperature", Value: 70, Quality: telemetry.QualityGood}}}
	<-states
	source.handles[0].frames <- telemetry.Frame{Samples: []telemetry.Sample{{MetricID: "temperature", Quality: telemetry.QualityUnavailable, Error: "timeout"}}}
	state := <-states
	if len(state.History["temperature"]) != 1 || state.History["temperature"][0].Value != 70 {
		t.Fatalf("history = %+v", state.History["temperature"])
	}
	if state.Current["temperature"].Quality != telemetry.QualityUnavailable {
		t.Fatalf("current = %+v", state.Current["temperature"])
	}
}
