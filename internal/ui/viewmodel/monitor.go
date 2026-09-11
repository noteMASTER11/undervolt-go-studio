package viewmodel

import (
	"sync"
	"time"

	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry/history"
)

const (
	minimumInterval = 100 * time.Millisecond
	maximumInterval = 5 * time.Second
	historyCapacity = 600
)

type SubscriptionHandle interface {
	Frames() <-chan telemetry.Frame
	Close()
}

type SubscriptionSource interface {
	Subscribe(telemetry.Subscription) SubscriptionHandle
}

type SchedulerSource struct {
	Scheduler *telemetry.Scheduler
}

func (s SchedulerSource) Subscribe(subscription telemetry.Subscription) SubscriptionHandle {
	return s.Scheduler.Subscribe(subscription)
}

type StateListener[T any] func(T)

type MonitorState struct {
	Active   bool
	Interval time.Duration
	Selected []telemetry.MetricID
	Current  map[telemetry.MetricID]telemetry.Sample
	History  map[telemetry.MetricID][]telemetry.Sample
}

type Monitor struct {
	source SubscriptionSource

	mu        sync.RWMutex
	active    bool
	interval  time.Duration
	metricIDs []telemetry.MetricID
	current   map[telemetry.MetricID]telemetry.Sample
	histories map[telemetry.MetricID]*history.Ring
	listener  StateListener[MonitorState]
	handle    SubscriptionHandle
	done      chan struct{}
}

func NewMonitor(source SubscriptionSource, interval time.Duration) *Monitor {
	return &Monitor{
		source: source, interval: clampInterval(interval),
		current:   make(map[telemetry.MetricID]telemetry.Sample),
		histories: make(map[telemetry.MetricID]*history.Ring),
	}
}

func (m *Monitor) Activate() {
	m.mu.Lock()
	if m.active {
		m.mu.Unlock()
		return
	}
	m.active = true
	handle := m.source.Subscribe(telemetry.Subscription{
		ConsumerID: "ui.monitor", MetricIDs: append([]telemetry.MetricID(nil), m.metricIDs...), Interval: m.interval,
	})
	done := make(chan struct{})
	m.handle = handle
	m.done = done
	m.mu.Unlock()
	go m.consume(handle, done)
}

func (m *Monitor) Deactivate() {
	m.mu.Lock()
	if !m.active {
		m.mu.Unlock()
		return
	}
	m.active = false
	handle, done := m.handle, m.done
	m.handle, m.done = nil, nil
	m.mu.Unlock()
	handle.Close()
	<-done
}

func (m *Monitor) SetInterval(interval time.Duration) {
	interval = clampInterval(interval)
	m.mu.Lock()
	if m.interval == interval {
		m.mu.Unlock()
		return
	}
	m.interval = interval
	active := m.active
	m.mu.Unlock()
	if active {
		m.restart()
	} else {
		m.publish()
	}
}

func (m *Monitor) Interval() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.interval
}

func (m *Monitor) SetMetricIDs(metricIDs []telemetry.MetricID) {
	metricIDs = uniqueMetricIDs(metricIDs)
	m.mu.Lock()
	m.metricIDs = metricIDs
	for _, metricID := range metricIDs {
		if m.histories[metricID] == nil {
			m.histories[metricID] = history.New(historyCapacity)
		}
	}
	active := m.active
	m.mu.Unlock()
	if active {
		m.restart()
	} else {
		m.publish()
	}
}

func (m *Monitor) SetListener(listener StateListener[MonitorState]) {
	m.mu.Lock()
	m.listener = listener
	m.mu.Unlock()
}

func (m *Monitor) State() MonitorState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stateLocked()
}

func (m *Monitor) restart() {
	m.Deactivate()
	m.Activate()
}

func (m *Monitor) consume(handle SubscriptionHandle, done chan struct{}) {
	defer close(done)
	for frame := range handle.Frames() {
		m.mu.Lock()
		for _, sample := range frame.Samples {
			m.current[sample.MetricID] = sample
			ring := m.histories[sample.MetricID]
			if ring == nil {
				ring = history.New(historyCapacity)
				m.histories[sample.MetricID] = ring
			}
			ring.Append(sample)
		}
		state, listener := m.stateLocked(), m.listener
		m.mu.Unlock()
		if listener != nil {
			listener(state)
		}
	}
}

func (m *Monitor) publish() {
	m.mu.RLock()
	state, listener := m.stateLocked(), m.listener
	m.mu.RUnlock()
	if listener != nil {
		listener(state)
	}
}

func (m *Monitor) stateLocked() MonitorState {
	state := MonitorState{
		Active: m.active, Interval: m.interval,
		Selected: append([]telemetry.MetricID(nil), m.metricIDs...),
		Current:  make(map[telemetry.MetricID]telemetry.Sample, len(m.current)),
		History:  make(map[telemetry.MetricID][]telemetry.Sample, len(m.histories)),
	}
	for _, metricID := range m.metricIDs {
		if sample, exists := m.current[metricID]; exists {
			state.Current[metricID] = sample
		}
		if ring := m.histories[metricID]; ring != nil {
			state.History[metricID] = history.DownsampleMinMax(ring.Snapshot(), 240)
		}
	}
	return state
}

func clampInterval(interval time.Duration) time.Duration {
	if interval < minimumInterval {
		return minimumInterval
	}
	if interval > maximumInterval {
		return maximumInterval
	}
	return interval
}

func uniqueMetricIDs(metricIDs []telemetry.MetricID) []telemetry.MetricID {
	seen := make(map[telemetry.MetricID]struct{}, len(metricIDs))
	result := make([]telemetry.MetricID, 0, len(metricIDs))
	for _, metricID := range metricIDs {
		if _, exists := seen[metricID]; exists {
			continue
		}
		seen[metricID] = struct{}{}
		result = append(result, metricID)
	}
	return result
}
