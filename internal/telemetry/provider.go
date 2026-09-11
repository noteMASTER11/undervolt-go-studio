package telemetry

import (
	"context"
	"time"
)

// Subscription declares the metrics and maximum desired interval for one consumer.
type Subscription struct {
	ConsumerID string
	MetricIDs  []MetricID
	Interval   time.Duration
}

// Provider discovers and samples a hardware telemetry source in batches.
type Provider interface {
	ID() string
	Discover(context.Context) (Catalog, error)
	Sample(context.Context, []MetricID) (Frame, error)
}
