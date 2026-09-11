package telemetry

import (
	"context"
	"testing"
	"time"
)

type deliveryProvider struct{}

func (deliveryProvider) ID() string                                        { return "delivery" }
func (deliveryProvider) Discover(context.Context) (Catalog, error)         { return Catalog{}, nil }
func (deliveryProvider) Sample(context.Context, []MetricID) (Frame, error) { return Frame{}, nil }

func TestDeliveryCadenceIsIndependentPerMetric(t *testing.T) {
	scheduler := NewScheduler([]Provider{deliveryProvider{}}, SchedulerOptions{})
	handle := scheduler.Subscribe(Subscription{
		ConsumerID: "monitor", MetricIDs: []MetricID{"fast", "slow"}, Interval: 300 * time.Millisecond,
	})
	defer handle.Close()
	runtime := scheduler.runtimes["delivery"]
	start := time.Unix(1, 0)
	publish := func(offset time.Duration, metricIDs ...MetricID) {
		frame := Frame{ProviderID: "delivery", FinishedAt: start.Add(offset)}
		for _, metricID := range metricIDs {
			frame.Samples = append(frame.Samples, Sample{MetricID: metricID, Value: 1, Quality: QualityGood})
		}
		scheduler.publish(runtime, frame)
	}
	receive := func() Frame {
		t.Helper()
		select {
		case frame := <-handle.Frames():
			return frame
		default:
			t.Fatal("expected delivered frame")
			return Frame{}
		}
	}

	publish(0, "fast", "slow")
	receive()
	for _, offset := range []time.Duration{300 * time.Millisecond, 600 * time.Millisecond, 900 * time.Millisecond} {
		publish(offset, "fast")
		receive()
	}
	publish(time.Second, "slow")
	frame := receive()
	if len(frame.Samples) != 1 || frame.Samples[0].MetricID != "slow" {
		t.Fatalf("slow frame = %+v", frame)
	}
}
