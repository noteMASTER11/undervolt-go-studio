package telemetry

import "time"

// ProviderDiagnostics is a point-in-time provider health snapshot.
type ProviderDiagnostics struct {
	ProviderID      string
	State           string
	LastSuccess     time.Time
	LastError       string
	LastLatency     time.Duration
	ActiveMetrics   int
	ActiveConsumers int
	DroppedFrames   uint64
}
