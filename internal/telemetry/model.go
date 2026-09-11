package telemetry

import (
	"time"

	"github.com/noteMASTER11/undervolt-go-studio/internal/hardware"
)

type MetricID string
type Unit string
type Quality string

const (
	QualityGood        Quality = "good"
	QualityStale       Quality = "stale"
	QualityUnavailable Quality = "unavailable"
)

// Descriptor declares one telemetry stream and its safe polling cadence.
type Descriptor struct {
	ID          MetricID
	ProviderID  string
	DeviceID    hardware.DeviceID
	Label       string
	Unit        Unit
	MinInterval time.Duration
}

// Sample is one timestamped metric value.
type Sample struct {
	MetricID  MetricID
	Value     float64
	Timestamp time.Time
	Quality   Quality
	Error     string
}

// QualityAt converts an expired good value to stale without hiding explicit failures.
func (s Sample) QualityAt(now time.Time, freshness time.Duration) Quality {
	if s.Quality != QualityGood {
		return s.Quality
	}
	if now.Sub(s.Timestamp) > freshness {
		return QualityStale
	}
	return QualityGood
}

// Frame is a batch produced by one provider invocation.
type Frame struct {
	ProviderID string
	StartedAt  time.Time
	FinishedAt time.Time
	Samples    []Sample
}

// Catalog is the discovered device and metric inventory.
type Catalog struct {
	Devices []hardware.Device
	Metrics []Descriptor
}
