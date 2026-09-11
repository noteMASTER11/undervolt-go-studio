package telemetry_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/noteMASTER11/undervolt-go-studio/internal/providers/fake"
	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
)

func testOptions() telemetry.SchedulerOptions {
	return telemetry.SchedulerOptions{
		ProviderTimeout:    50 * time.Millisecond,
		AllowTestIntervals: true,
	}
}

func TestSchedulerDoesNotPollWithoutSubscribers(t *testing.T) {
	provider := fake.New(fake.Options{ProviderID: "fake", MetricCount: 3})
	scheduler := telemetry.NewScheduler([]telemetry.Provider{provider}, testOptions())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := scheduler.Discover(ctx); err != nil {
		t.Fatal(err)
	}
	scheduler.Start(ctx)
	time.Sleep(30 * time.Millisecond)
	if got := provider.SampleCalls(); got != 0 {
		t.Fatalf("sample calls = %d, want 0", got)
	}
}

func TestSchedulerCatalogReturnsCopy(t *testing.T) {
	provider := fake.New(fake.Options{ProviderID: "fake", MetricCount: 1})
	scheduler := telemetry.NewScheduler([]telemetry.Provider{provider}, testOptions())
	if _, err := scheduler.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := scheduler.Catalog()
	first.Metrics[0].Label = "changed"
	if got := scheduler.Catalog().Metrics[0].Label; got == "changed" {
		t.Fatal("Catalog returned mutable scheduler storage")
	}
}

func TestSchedulerBatchesRequestedMetrics(t *testing.T) {
	provider := fake.New(fake.Options{ProviderID: "fake", MetricCount: 3})
	scheduler := telemetry.NewScheduler([]telemetry.Provider{provider}, testOptions())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := scheduler.Discover(ctx); err != nil {
		t.Fatal(err)
	}
	scheduler.Start(ctx)
	handle := scheduler.Subscribe(telemetry.Subscription{
		ConsumerID: "monitor",
		MetricIDs:  []telemetry.MetricID{"fake.metric.0", "fake.metric.1"},
		Interval:   10 * time.Millisecond,
	})
	defer handle.Close()

	select {
	case frame := <-handle.Frames():
		if len(frame.Samples) != 2 {
			t.Fatalf("samples = %d, want 2", len(frame.Samples))
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("no frame received")
	}
	if got := provider.LastBatchSize(); got != 2 {
		t.Fatalf("batch size = %d, want 2", got)
	}
}

func TestSlowProviderDoesNotBlockFastProvider(t *testing.T) {
	slow := fake.New(fake.Options{ProviderID: "slow", MetricCount: 1, Delay: 200 * time.Millisecond})
	fast := fake.New(fake.Options{ProviderID: "fast", MetricCount: 1})
	options := testOptions()
	options.ProviderTimeout = 25 * time.Millisecond
	scheduler := telemetry.NewScheduler([]telemetry.Provider{slow, fast}, options)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := scheduler.Discover(ctx); err != nil {
		t.Fatal(err)
	}
	scheduler.Start(ctx)
	handle := scheduler.Subscribe(telemetry.Subscription{
		ConsumerID: "overview",
		MetricIDs:  []telemetry.MetricID{"slow.metric.0", "fast.metric.0"},
		Interval:   10 * time.Millisecond,
	})
	defer handle.Close()

	deadline := time.After(200 * time.Millisecond)
	for {
		select {
		case frame := <-handle.Frames():
			if frame.ProviderID == "fast" {
				return
			}
		case <-deadline:
			t.Fatal("fast provider was blocked by slow provider")
		}
	}
}

func TestProviderFailurePublishesUnavailableSamples(t *testing.T) {
	provider := fake.New(fake.Options{ProviderID: "failing", MetricCount: 1, FailEvery: 2})
	scheduler := telemetry.NewScheduler([]telemetry.Provider{provider}, testOptions())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := scheduler.Discover(ctx); err != nil {
		t.Fatal(err)
	}
	scheduler.Start(ctx)
	handle := scheduler.Subscribe(telemetry.Subscription{
		ConsumerID: "monitor", MetricIDs: []telemetry.MetricID{"failing.metric.0"}, Interval: time.Millisecond,
	})
	defer handle.Close()
	select {
	case <-handle.Frames():
	case <-time.After(time.Second):
		t.Fatal("no initial frame")
	}
	select {
	case frame := <-handle.Frames():
		if len(frame.Samples) != 1 || frame.Samples[0].Quality != telemetry.QualityUnavailable || frame.Samples[0].Error == "" {
			t.Fatalf("failure frame = %+v", frame)
		}
	case <-time.After(time.Second):
		t.Fatal("provider failure was not published")
	}
}

type discoveryProvider struct {
	id    string
	delay time.Duration
}

func (p discoveryProvider) ID() string { return p.id }
func (p discoveryProvider) Discover(ctx context.Context) (telemetry.Catalog, error) {
	select {
	case <-time.After(p.delay):
		return telemetry.Catalog{Metrics: []telemetry.Descriptor{{ID: telemetry.MetricID(p.id + ".metric"), ProviderID: p.id}}}, nil
	case <-ctx.Done():
		return telemetry.Catalog{}, ctx.Err()
	}
}
func (p discoveryProvider) Sample(context.Context, []telemetry.MetricID) (telemetry.Frame, error) {
	return telemetry.Frame{}, fmt.Errorf("not implemented")
}

func TestDiscoveryRunsProvidersConcurrentlyWithDeadlines(t *testing.T) {
	scheduler := telemetry.NewScheduler([]telemetry.Provider{
		discoveryProvider{id: "slow", delay: 100 * time.Millisecond},
		discoveryProvider{id: "fast"},
	}, telemetry.SchedulerOptions{ProviderTimeout: 20 * time.Millisecond})
	started := time.Now()
	catalog, err := scheduler.Discover(context.Background())
	if err == nil {
		t.Fatal("slow discovery did not report its deadline")
	}
	if elapsed := time.Since(started); elapsed > 70*time.Millisecond {
		t.Fatalf("discovery took %s; providers ran serially or without deadline", elapsed)
	}
	if len(catalog.Metrics) != 1 || catalog.Metrics[0].ID != "fast.metric" {
		t.Fatalf("catalog = %+v", catalog)
	}
}

type cadenceProvider struct {
	mu      sync.Mutex
	metrics []telemetry.Descriptor
	counts  map[telemetry.MetricID]int
}

func (p *cadenceProvider) ID() string { return "cadence" }
func (p *cadenceProvider) Discover(context.Context) (telemetry.Catalog, error) {
	return telemetry.Catalog{Metrics: append([]telemetry.Descriptor(nil), p.metrics...)}, nil
}
func (p *cadenceProvider) Sample(_ context.Context, metricIDs []telemetry.MetricID) (telemetry.Frame, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	frame := telemetry.Frame{ProviderID: p.ID(), StartedAt: now, FinishedAt: now}
	for _, metricID := range metricIDs {
		p.counts[metricID]++
		frame.Samples = append(frame.Samples, telemetry.Sample{MetricID: metricID, Value: 1, Timestamp: now, Quality: telemetry.QualityGood})
	}
	return frame, nil
}
func (p *cadenceProvider) count(metricID telemetry.MetricID) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.counts[metricID]
}

func TestSchedulerHonorsPerMetricMinimumIntervals(t *testing.T) {
	provider := &cadenceProvider{counts: make(map[telemetry.MetricID]int), metrics: []telemetry.Descriptor{
		{ID: "fast", ProviderID: "cadence", MinInterval: time.Millisecond},
		{ID: "slow", ProviderID: "cadence", MinInterval: 40 * time.Millisecond},
	}}
	scheduler := telemetry.NewScheduler([]telemetry.Provider{provider}, telemetry.SchedulerOptions{MinInterval: time.Millisecond, MaxInterval: time.Second, AllowTestIntervals: true})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := scheduler.Discover(ctx); err != nil {
		t.Fatal(err)
	}
	scheduler.Start(ctx)
	handle := scheduler.Subscribe(telemetry.Subscription{ConsumerID: "monitor", MetricIDs: []telemetry.MetricID{"fast", "slow"}, Interval: time.Millisecond})
	time.Sleep(20 * time.Millisecond)
	handle.Close()
	if fast := provider.count("fast"); fast < 3 {
		t.Fatalf("fast samples = %d", fast)
	}
	if slow := provider.count("slow"); slow != 1 {
		t.Fatalf("slow samples = %d, want 1", slow)
	}
}

func TestSchedulerHonorsEachSubscriberDeliveryInterval(t *testing.T) {
	provider := &cadenceProvider{counts: make(map[telemetry.MetricID]int), metrics: []telemetry.Descriptor{{ID: "metric", ProviderID: "cadence", MinInterval: time.Millisecond}}}
	scheduler := telemetry.NewScheduler([]telemetry.Provider{provider}, telemetry.SchedulerOptions{MinInterval: time.Millisecond, MaxInterval: time.Second, AllowTestIntervals: true})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := scheduler.Discover(ctx); err != nil {
		t.Fatal(err)
	}
	scheduler.Start(ctx)
	fast := scheduler.Subscribe(telemetry.Subscription{ConsumerID: "fast", MetricIDs: []telemetry.MetricID{"metric"}, Interval: time.Millisecond})
	slow := scheduler.Subscribe(telemetry.Subscription{ConsumerID: "slow", MetricIDs: []telemetry.MetricID{"metric"}, Interval: 40 * time.Millisecond})
	defer fast.Close()
	defer slow.Close()
	deadline := time.After(20 * time.Millisecond)
	fastFrames, slowFrames := 0, 0
loop:
	for {
		select {
		case <-fast.Frames():
			fastFrames++
		case <-slow.Frames():
			slowFrames++
		case <-deadline:
			break loop
		}
	}
	if fastFrames < 3 {
		t.Fatalf("fast frames = %d", fastFrames)
	}
	if slowFrames != 1 {
		t.Fatalf("slow frames = %d, want 1", slowFrames)
	}
}

func TestHandleCloseIsIdempotentAndStopsDemand(t *testing.T) {
	provider := fake.New(fake.Options{ProviderID: "fake", MetricCount: 1})
	scheduler := telemetry.NewScheduler([]telemetry.Provider{provider}, testOptions())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := scheduler.Discover(ctx); err != nil {
		t.Fatal(err)
	}
	scheduler.Start(ctx)
	handle := scheduler.Subscribe(telemetry.Subscription{
		ConsumerID: "monitor",
		MetricIDs:  []telemetry.MetricID{"fake.metric.0"},
		Interval:   5 * time.Millisecond,
	})
	select {
	case <-handle.Frames():
	case <-time.After(200 * time.Millisecond):
		t.Fatal("no initial frame")
	}
	handle.Close()
	handle.Close()
	time.Sleep(20 * time.Millisecond)
	diagnostics := scheduler.Diagnostics()
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %d, want 1", len(diagnostics))
	}
	if diagnostics[0].ActiveConsumers != 0 || diagnostics[0].ActiveMetrics != 0 {
		t.Fatalf("demand remained after close: %+v", diagnostics[0])
	}
}
