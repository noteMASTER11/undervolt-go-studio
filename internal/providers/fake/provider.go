package fake

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/noteMASTER11/undervolt-go-studio/internal/hardware"
	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
)

// Options configures a deterministic telemetry provider for tests and demos.
type Options struct {
	ProviderID  string
	MetricCount int
	Delay       time.Duration
	FailEvery   int
}

// Provider emits deterministic values without touching hardware.
type Provider struct {
	options       Options
	metricIndexes map[telemetry.MetricID]int
	sampleCalls   atomic.Int64
	lastBatchSize atomic.Int64
}

// New creates a deterministic provider with MetricCount numbered metrics.
func New(options Options) *Provider {
	indexes := make(map[telemetry.MetricID]int, options.MetricCount)
	for index := 0; index < options.MetricCount; index++ {
		id := telemetry.MetricID(fmt.Sprintf("%s.metric.%d", options.ProviderID, index))
		indexes[id] = index
	}
	return &Provider{options: options, metricIndexes: indexes}
}

func (p *Provider) ID() string {
	return p.options.ProviderID
}

func (p *Provider) Discover(context.Context) (telemetry.Catalog, error) {
	deviceID := hardware.NewDeviceID(hardware.KindCPU, "fake", p.options.ProviderID)
	metrics := make([]telemetry.Descriptor, 0, p.options.MetricCount)
	for index := 0; index < p.options.MetricCount; index++ {
		metrics = append(metrics, telemetry.Descriptor{
			ID:          telemetry.MetricID(fmt.Sprintf("%s.metric.%d", p.options.ProviderID, index)),
			ProviderID:  p.options.ProviderID,
			DeviceID:    deviceID,
			Label:       fmt.Sprintf("Metric %d", index),
			Unit:        "unit",
			MinInterval: time.Millisecond,
		})
	}
	return telemetry.Catalog{
		Devices: []hardware.Device{{ID: deviceID, Kind: hardware.KindCPU, Vendor: "fake", Name: p.options.ProviderID}},
		Metrics: metrics,
	}, nil
}

func (p *Provider) Sample(ctx context.Context, metricIDs []telemetry.MetricID) (telemetry.Frame, error) {
	started := time.Now()
	call := p.sampleCalls.Add(1)
	p.lastBatchSize.Store(int64(len(metricIDs)))

	if p.options.Delay > 0 {
		timer := time.NewTimer(p.options.Delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return telemetry.Frame{}, ctx.Err()
		case <-timer.C:
		}
	}
	if p.options.FailEvery > 0 && call%int64(p.options.FailEvery) == 0 {
		return telemetry.Frame{}, fmt.Errorf("fake provider %s failed on call %d", p.options.ProviderID, call)
	}

	samples := make([]telemetry.Sample, 0, len(metricIDs))
	for _, id := range metricIDs {
		index, ok := p.metricIndexes[id]
		if !ok {
			samples = append(samples, telemetry.Sample{MetricID: id, Timestamp: started, Quality: telemetry.QualityUnavailable, Error: "unknown fake metric"})
			continue
		}
		samples = append(samples, telemetry.Sample{
			MetricID:  id,
			Value:     float64(call*1000 + int64(index)),
			Timestamp: started,
			Quality:   telemetry.QualityGood,
		})
	}
	return telemetry.Frame{ProviderID: p.ID(), StartedAt: started, FinishedAt: time.Now(), Samples: samples}, nil
}

func (p *Provider) SampleCalls() int64 {
	return p.sampleCalls.Load()
}

func (p *Provider) LastBatchSize() int {
	return int(p.lastBatchSize.Load())
}
