package telemetry

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/noteMASTER11/undervolt-go-studio/internal/hardware"
)

const (
	defaultInterval = 250 * time.Millisecond
	defaultTimeout  = time.Second
	firstBackoff    = 250 * time.Millisecond
	maximumBackoff  = 5 * time.Second
)

// SchedulerOptions controls polling boundaries and provider deadlines.
type SchedulerOptions struct {
	ProviderTimeout    time.Duration
	MinInterval        time.Duration
	MaxInterval        time.Duration
	AllowTestIntervals bool
}

// Handle owns one scheduler subscription.
type Handle struct {
	frames    chan Frame
	closeOnce sync.Once
	closeFunc func()
}

func (h *Handle) Frames() <-chan Frame {
	return h.frames
}

func (h *Handle) Close() {
	if h == nil {
		return
	}
	h.closeOnce.Do(h.closeFunc)
}

type subscriptionEntry struct {
	subscription Subscription
	metricSet    map[MetricID]struct{}
	handle       *Handle
}

type providerRuntime struct {
	provider Provider
	wake     chan struct{}

	mu          sync.Mutex
	diagnostics ProviderDiagnostics
}

func (r *providerRuntime) update(update func(*ProviderDiagnostics)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	update(&r.diagnostics)
}

func (r *providerRuntime) snapshot() ProviderDiagnostics {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.diagnostics
}

// Scheduler samples providers only in response to active subscriptions.
type Scheduler struct {
	options SchedulerOptions

	mu             sync.RWMutex
	runtimes       map[string]*providerRuntime
	providerOrder  []string
	metricProvider map[MetricID]string
	descriptors    map[MetricID]Descriptor
	catalog        Catalog
	subscriptions  map[uint64]*subscriptionEntry
	nextID         uint64
	started        bool
	stopped        bool
	waitGroup      sync.WaitGroup
}

// NewScheduler creates an idle scheduler. Discover and Start are explicit.
func NewScheduler(providers []Provider, options SchedulerOptions) *Scheduler {
	if options.ProviderTimeout <= 0 {
		options.ProviderTimeout = defaultTimeout
	}
	if options.MinInterval <= 0 {
		if options.AllowTestIntervals {
			options.MinInterval = time.Millisecond
		} else {
			options.MinInterval = 100 * time.Millisecond
		}
	}
	if options.MaxInterval <= 0 {
		options.MaxInterval = 5 * time.Second
	}

	scheduler := &Scheduler{
		options:        options,
		runtimes:       make(map[string]*providerRuntime, len(providers)),
		metricProvider: make(map[MetricID]string),
		descriptors:    make(map[MetricID]Descriptor),
		subscriptions:  make(map[uint64]*subscriptionEntry),
	}
	for _, provider := range providers {
		if provider == nil {
			continue
		}
		id := provider.ID()
		if _, exists := scheduler.runtimes[id]; exists {
			continue
		}
		scheduler.providerOrder = append(scheduler.providerOrder, id)
		scheduler.runtimes[id] = &providerRuntime{
			provider: provider,
			wake:     make(chan struct{}, 1),
			diagnostics: ProviderDiagnostics{
				ProviderID: id,
				State:      "idle",
			},
		}
	}
	return scheduler
}

// Discover builds the metric-to-provider routing catalog.
func (s *Scheduler) Discover(ctx context.Context) (Catalog, error) {
	var combined Catalog
	metricProvider := make(map[MetricID]string)
	descriptors := make(map[MetricID]Descriptor)
	var discoverErrors []error

	for _, id := range s.providerOrder {
		runtime := s.runtimes[id]
		catalog, err := runtime.provider.Discover(ctx)
		if err != nil {
			discoverErrors = append(discoverErrors, fmt.Errorf("discover %s: %w", id, err))
			runtime.update(func(diagnostics *ProviderDiagnostics) {
				diagnostics.LastError = err.Error()
			})
			continue
		}
		combined.Devices = append(combined.Devices, catalog.Devices...)
		for _, descriptor := range catalog.Metrics {
			if descriptor.ProviderID == "" {
				descriptor.ProviderID = id
			}
			if previous, exists := metricProvider[descriptor.ID]; exists {
				discoverErrors = append(discoverErrors, fmt.Errorf("metric %s belongs to both %s and %s", descriptor.ID, previous, id))
				continue
			}
			metricProvider[descriptor.ID] = id
			descriptors[descriptor.ID] = descriptor
			combined.Metrics = append(combined.Metrics, descriptor)
		}
	}

	s.mu.Lock()
	s.metricProvider = metricProvider
	s.descriptors = descriptors
	s.catalog = cloneCatalog(combined)
	s.mu.Unlock()

	return cloneCatalog(combined), errors.Join(discoverErrors...)
}

// Start launches one isolated worker per provider.
func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	for _, id := range s.providerOrder {
		runtime := s.runtimes[id]
		s.waitGroup.Add(1)
		go func() {
			defer s.waitGroup.Done()
			s.runProvider(ctx, runtime)
		}()
	}
	s.mu.Unlock()

	go func() {
		<-ctx.Done()
		s.waitGroup.Wait()
		s.closeAllSubscriptions()
	}()
}

// Subscribe registers demand and returns a capacity-one latest-value stream.
func (s *Scheduler) Subscribe(subscription Subscription) *Handle {
	subscription.Interval = s.clampInterval(subscription.Interval)
	subscription.MetricIDs = uniqueMetricIDs(subscription.MetricIDs)
	metricSet := make(map[MetricID]struct{}, len(subscription.MetricIDs))
	for _, metricID := range subscription.MetricIDs {
		metricSet[metricID] = struct{}{}
	}

	handle := &Handle{frames: make(chan Frame, 1)}
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	handle.closeFunc = func() { s.unsubscribe(id) }
	if s.stopped {
		close(handle.frames)
		handle.closeFunc = func() {}
		s.mu.Unlock()
		return handle
	}
	s.subscriptions[id] = &subscriptionEntry{subscription: subscription, metricSet: metricSet, handle: handle}
	s.notifyWorkersLocked()
	s.mu.Unlock()
	return handle
}

// Diagnostics returns provider health plus current demand counts.
func (s *Scheduler) Diagnostics() []ProviderDiagnostics {
	result := make([]ProviderDiagnostics, 0, len(s.providerOrder))
	for _, id := range s.providerOrder {
		diagnostics := s.runtimes[id].snapshot()
		metrics, _, consumers := s.demandFor(id)
		diagnostics.ActiveMetrics = len(metrics)
		diagnostics.ActiveConsumers = consumers
		result = append(result, diagnostics)
	}
	return result
}

func (s *Scheduler) runProvider(ctx context.Context, runtime *providerRuntime) {
	backoff := firstBackoff
	for {
		metricIDs, interval, _ := s.demandFor(runtime.provider.ID())
		if len(metricIDs) == 0 {
			runtime.update(func(diagnostics *ProviderDiagnostics) { diagnostics.State = "idle" })
			select {
			case <-ctx.Done():
				runtime.update(func(diagnostics *ProviderDiagnostics) { diagnostics.State = "stopped" })
				return
			case <-runtime.wake:
				continue
			}
		}

		runtime.update(func(diagnostics *ProviderDiagnostics) { diagnostics.State = "running" })
		started := time.Now()
		sampleCtx, cancel := context.WithTimeout(ctx, s.options.ProviderTimeout)
		frame, err := runtime.provider.Sample(sampleCtx, metricIDs)
		cancel()
		latency := time.Since(started)
		if err != nil {
			runtime.update(func(diagnostics *ProviderDiagnostics) {
				diagnostics.State = "backoff"
				diagnostics.LastError = err.Error()
				diagnostics.LastLatency = latency
			})
			if !waitForWake(ctx, runtime.wake, backoff) {
				runtime.update(func(diagnostics *ProviderDiagnostics) { diagnostics.State = "stopped" })
				return
			}
			backoff *= 2
			if backoff > maximumBackoff {
				backoff = maximumBackoff
			}
			continue
		}

		backoff = firstBackoff
		runtime.update(func(diagnostics *ProviderDiagnostics) {
			diagnostics.State = "running"
			diagnostics.LastSuccess = time.Now()
			diagnostics.LastError = ""
			diagnostics.LastLatency = latency
		})
		s.publish(runtime, frame)
		if !waitForWake(ctx, runtime.wake, interval) {
			runtime.update(func(diagnostics *ProviderDiagnostics) { diagnostics.State = "stopped" })
			return
		}
	}
}

func (s *Scheduler) publish(runtime *providerRuntime, frame Frame) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, entry := range s.subscriptions {
		filtered := frame
		filtered.Samples = make([]Sample, 0, len(frame.Samples))
		for _, sample := range frame.Samples {
			if _, requested := entry.metricSet[sample.MetricID]; requested {
				filtered.Samples = append(filtered.Samples, sample)
			}
		}
		if len(filtered.Samples) == 0 {
			continue
		}
		if deliverLatest(entry.handle.frames, filtered) {
			runtime.update(func(diagnostics *ProviderDiagnostics) { diagnostics.DroppedFrames++ })
		}
	}
}

func (s *Scheduler) demandFor(providerID string) ([]MetricID, time.Duration, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	metricSet := make(map[MetricID]struct{})
	interval := s.options.MaxInterval
	consumers := 0
	for _, entry := range s.subscriptions {
		consumerUsesProvider := false
		for _, metricID := range entry.subscription.MetricIDs {
			if s.metricProvider[metricID] != providerID {
				continue
			}
			metricSet[metricID] = struct{}{}
			consumerUsesProvider = true
			effective := entry.subscription.Interval
			if descriptor := s.descriptors[metricID]; descriptor.MinInterval > effective {
				effective = descriptor.MinInterval
			}
			if effective < interval {
				interval = effective
			}
		}
		if consumerUsesProvider {
			consumers++
		}
	}
	metricIDs := make([]MetricID, 0, len(metricSet))
	for metricID := range metricSet {
		metricIDs = append(metricIDs, metricID)
	}
	sort.Slice(metricIDs, func(i, j int) bool { return metricIDs[i] < metricIDs[j] })
	return metricIDs, interval, consumers
}

func (s *Scheduler) unsubscribe(id uint64) {
	s.mu.Lock()
	entry, exists := s.subscriptions[id]
	if exists {
		delete(s.subscriptions, id)
		close(entry.handle.frames)
		s.notifyWorkersLocked()
	}
	s.mu.Unlock()
}

func (s *Scheduler) closeAllSubscriptions() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	s.stopped = true
	for id, entry := range s.subscriptions {
		close(entry.handle.frames)
		delete(s.subscriptions, id)
	}
}

func (s *Scheduler) notifyWorkersLocked() {
	for _, runtime := range s.runtimes {
		select {
		case runtime.wake <- struct{}{}:
		default:
		}
	}
}

func (s *Scheduler) clampInterval(interval time.Duration) time.Duration {
	if interval <= 0 {
		interval = defaultInterval
	}
	if interval < s.options.MinInterval {
		return s.options.MinInterval
	}
	if interval > s.options.MaxInterval {
		return s.options.MaxInterval
	}
	return interval
}

func waitForWake(ctx context.Context, wake <-chan struct{}, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-wake:
		return true
	case <-timer.C:
		return true
	}
}

func uniqueMetricIDs(metricIDs []MetricID) []MetricID {
	seen := make(map[MetricID]struct{}, len(metricIDs))
	result := make([]MetricID, 0, len(metricIDs))
	for _, metricID := range metricIDs {
		if _, exists := seen[metricID]; exists {
			continue
		}
		seen[metricID] = struct{}{}
		result = append(result, metricID)
	}
	return result
}

func cloneCatalog(catalog Catalog) Catalog {
	return Catalog{
		Devices: append([]hardware.Device(nil), catalog.Devices...),
		Metrics: append([]Descriptor(nil), catalog.Metrics...),
	}
}
