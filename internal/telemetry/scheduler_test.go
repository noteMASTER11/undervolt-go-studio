package telemetry_test

import (
	"context"
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
