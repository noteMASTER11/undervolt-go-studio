package telemetry_test

import (
	"context"
	"testing"
	"time"

	"github.com/noteMASTER11/undervolt-go-studio/internal/providers/fake"
	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
)

func BenchmarkScheduler200Metrics(b *testing.B) {
	provider := fake.New(fake.Options{ProviderID: "bench", MetricCount: 200})
	scheduler := telemetry.NewScheduler([]telemetry.Provider{provider}, telemetry.SchedulerOptions{
		ProviderTimeout: time.Second, MinInterval: time.Millisecond, MaxInterval: time.Second, AllowTestIntervals: true,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	catalog, err := scheduler.Discover(ctx)
	if err != nil {
		b.Fatal(err)
	}
	metricIDs := make([]telemetry.MetricID, len(catalog.Metrics))
	for index, descriptor := range catalog.Metrics {
		metricIDs[index] = descriptor.ID
	}
	scheduler.Start(ctx)
	slow := scheduler.Subscribe(telemetry.Subscription{ConsumerID: "slow", MetricIDs: metricIDs, Interval: time.Millisecond})
	defer slow.Close()
	handle := scheduler.Subscribe(telemetry.Subscription{ConsumerID: "benchmark", MetricIDs: metricIDs, Interval: time.Millisecond})
	defer handle.Close()
	if capacity := cap(handle.Frames()); capacity != 1 {
		b.Fatalf("frame channel capacity = %d, want 1", capacity)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		select {
		case <-handle.Frames():
		case <-time.After(time.Second):
			b.Fatal("timed out waiting for telemetry frame")
		}
	}
}
