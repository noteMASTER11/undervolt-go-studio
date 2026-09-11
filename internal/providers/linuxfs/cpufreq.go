package linuxfs

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/noteMASTER11/undervolt-go-studio/internal/hardware"
	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
)

const cpuDirectory = "sys/devices/system/cpu"

type CPUFreq struct {
	filesystem FileSystem

	mu    sync.RWMutex
	paths map[telemetry.MetricID]string
}

func NewCPUFreq(root string) *CPUFreq {
	return &CPUFreq{filesystem: RootFS{Root: root}, paths: make(map[telemetry.MetricID]string)}
}

func (p *CPUFreq) ID() string {
	return "linux.cpufreq"
}

func (p *CPUFreq) Discover(context.Context) (telemetry.Catalog, error) {
	entries, err := p.filesystem.ReadDir(cpuDirectory)
	if err != nil {
		return telemetry.Catalog{}, err
	}
	type discoveredCPU struct {
		index int
		path  string
	}
	var cpus []discoveredCPU
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "cpu") {
			continue
		}
		index, err := strconv.Atoi(strings.TrimPrefix(entry.Name(), "cpu"))
		if err != nil {
			continue
		}
		path := fmt.Sprintf("%s/%s/cpufreq/scaling_cur_freq", cpuDirectory, entry.Name())
		if _, err := p.filesystem.ReadFile(path); err != nil {
			continue
		}
		cpus = append(cpus, discoveredCPU{index: index, path: path})
	}
	sort.Slice(cpus, func(i, j int) bool { return cpus[i].index < cpus[j].index })

	deviceID := hardware.NewDeviceID(hardware.KindCPU, "linux", "cpu")
	paths := make(map[telemetry.MetricID]string, len(cpus))
	metrics := make([]telemetry.Descriptor, 0, len(cpus))
	for _, cpu := range cpus {
		metricID := telemetry.MetricID(fmt.Sprintf("cpu.%d.frequency", cpu.index))
		paths[metricID] = cpu.path
		metrics = append(metrics, telemetry.Descriptor{
			ID:          metricID,
			ProviderID:  p.ID(),
			DeviceID:    deviceID,
			Label:       fmt.Sprintf("CPU %d Frequency", cpu.index),
			Unit:        "MHz",
			MinInterval: 100 * time.Millisecond,
		})
	}
	p.mu.Lock()
	p.paths = paths
	p.mu.Unlock()
	return telemetry.Catalog{
		Devices: []hardware.Device{{ID: deviceID, Kind: hardware.KindCPU, Vendor: "linux", Name: "CPU"}},
		Metrics: metrics,
	}, nil
}

func (p *CPUFreq) Sample(ctx context.Context, metricIDs []telemetry.MetricID) (telemetry.Frame, error) {
	started := time.Now()
	p.mu.RLock()
	paths := make(map[telemetry.MetricID]string, len(metricIDs))
	for _, metricID := range metricIDs {
		paths[metricID] = p.paths[metricID]
	}
	p.mu.RUnlock()

	samples := make([]telemetry.Sample, 0, len(metricIDs))
	for _, metricID := range metricIDs {
		if err := ctx.Err(); err != nil {
			return telemetry.Frame{}, err
		}
		path := paths[metricID]
		if path == "" {
			samples = append(samples, unavailable(metricID, started, "metric was not discovered"))
			continue
		}
		value, err := readFloat(p.filesystem, path)
		if err != nil {
			samples = append(samples, unavailable(metricID, started, err.Error()))
			continue
		}
		samples = append(samples, telemetry.Sample{MetricID: metricID, Value: value / 1000, Timestamp: started, Quality: telemetry.QualityGood})
	}
	return telemetry.Frame{ProviderID: p.ID(), StartedAt: started, FinishedAt: time.Now(), Samples: samples}, nil
}
