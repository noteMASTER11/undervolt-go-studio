package fake

import (
	"context"
	"testing"
	"time"

	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
)

func TestProviderSamplesRequestedMetricsDeterministically(t *testing.T) {
	provider := New(Options{ProviderID: "fake", MetricCount: 3})
	catalog, err := provider.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Metrics) != 3 {
		t.Fatalf("metrics = %d, want 3", len(catalog.Metrics))
	}

	frame, err := provider.Sample(context.Background(), []telemetry.MetricID{"fake.metric.0", "fake.metric.2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(frame.Samples) != 2 {
		t.Fatalf("samples = %d, want 2", len(frame.Samples))
	}
	if frame.Samples[0].Value != 1000 || frame.Samples[1].Value != 1002 {
		t.Fatalf("values = %v, want [1000 1002]", []float64{frame.Samples[0].Value, frame.Samples[1].Value})
	}
	if provider.SampleCalls() != 1 || provider.LastBatchSize() != 2 {
		t.Fatalf("calls = %d, batch = %d", provider.SampleCalls(), provider.LastBatchSize())
	}
}

func TestProviderDelayHonorsContextCancellation(t *testing.T) {
	provider := New(Options{ProviderID: "slow", MetricCount: 1, Delay: time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := provider.Sample(ctx, []telemetry.MetricID{"slow.metric.0"})
	if err != context.DeadlineExceeded {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
}
