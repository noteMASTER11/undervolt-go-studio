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

type cpuCounters struct {
	total uint64
	idle  uint64
}

type ProcStat struct {
	filesystem FileSystem

	mu        sync.Mutex
	previous  map[string]cpuCounters
	metricCPU map[telemetry.MetricID]string
}

func NewProcStat(root string) *ProcStat {
	return &ProcStat{
		filesystem: RootFS{Root: root},
		previous:   make(map[string]cpuCounters),
		metricCPU:  make(map[telemetry.MetricID]string),
	}
}

func (p *ProcStat) ID() string {
	return "linux.procstat"
}

func (p *ProcStat) Discover(context.Context) (telemetry.Catalog, error) {
	data, err := p.filesystem.ReadFile("proc/stat")
	if err != nil {
		return telemetry.Catalog{}, err
	}
	counters, err := parseProcStat(data)
	if err != nil {
		return telemetry.Catalog{}, err
	}
	names := make([]string, 0, len(counters))
	for name := range counters {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if names[i] == "cpu" {
			return true
		}
		if names[j] == "cpu" {
			return false
		}
		left, leftErr := strconv.Atoi(strings.TrimPrefix(names[i], "cpu"))
		right, rightErr := strconv.Atoi(strings.TrimPrefix(names[j], "cpu"))
		if leftErr == nil && rightErr == nil {
			return left < right
		}
		return names[i] < names[j]
	})
	deviceID := hardware.NewDeviceID(hardware.KindCPU, "linux", "cpu")
	metricCPU := make(map[telemetry.MetricID]string, len(names))
	descriptors := make([]telemetry.Descriptor, 0, len(names))
	for _, name := range names {
		metricID := telemetry.MetricID(name + ".utilization")
		metricCPU[metricID] = name
		label := "CPU Utilization"
		if name != "cpu" {
			label = strings.ToUpper(name) + " Utilization"
		}
		descriptors = append(descriptors, telemetry.Descriptor{
			ID: metricID, ProviderID: p.ID(), DeviceID: deviceID, Label: label,
			Unit: "%", MinInterval: 100 * time.Millisecond,
		})
	}
	p.mu.Lock()
	p.metricCPU = metricCPU
	p.previous = make(map[string]cpuCounters)
	p.mu.Unlock()
	return telemetry.Catalog{
		Devices: []hardware.Device{{ID: deviceID, Kind: hardware.KindCPU, Vendor: "linux", Name: "CPU"}},
		Metrics: descriptors,
	}, nil
}

func (p *ProcStat) Sample(ctx context.Context, metricIDs []telemetry.MetricID) (telemetry.Frame, error) {
	started := time.Now()
	if err := ctx.Err(); err != nil {
		return telemetry.Frame{}, err
	}
	data, err := p.filesystem.ReadFile("proc/stat")
	if err != nil {
		return telemetry.Frame{}, err
	}
	current, err := parseProcStat(data)
	if err != nil {
		return telemetry.Frame{}, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	samples := make([]telemetry.Sample, 0, len(metricIDs))
	for _, metricID := range metricIDs {
		name := p.metricCPU[metricID]
		counters, exists := current[name]
		if name == "" || !exists {
			samples = append(samples, unavailable(metricID, started, "metric was not discovered"))
			continue
		}
		previous, hasPrevious := p.previous[name]
		if !hasPrevious {
			samples = append(samples, unavailable(metricID, started, "utilization requires two snapshots"))
			continue
		}
		if counters.total < previous.total || counters.idle < previous.idle {
			samples = append(samples, unavailable(metricID, started, "CPU counters regressed"))
			continue
		}
		deltaTotal := counters.total - previous.total
		deltaIdle := counters.idle - previous.idle
		if deltaTotal == 0 || deltaIdle > deltaTotal {
			samples = append(samples, unavailable(metricID, started, "CPU counter delta is invalid"))
			continue
		}
		utilization := float64(deltaTotal-deltaIdle) / float64(deltaTotal) * 100
		samples = append(samples, telemetry.Sample{MetricID: metricID, Value: utilization, Timestamp: started, Quality: telemetry.QualityGood})
	}
	for name, counters := range current {
		p.previous[name] = counters
	}
	return telemetry.Frame{ProviderID: p.ID(), StartedAt: started, FinishedAt: time.Now(), Samples: samples}, nil
}

func parseProcStat(data []byte) (map[string]cpuCounters, error) {
	result := make(map[string]cpuCounters)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || (fields[0] != "cpu" && !strings.HasPrefix(fields[0], "cpu")) {
			continue
		}
		if len(fields) < 5 {
			return nil, fmt.Errorf("invalid /proc/stat CPU line %q", line)
		}
		var values []uint64
		for _, field := range fields[1:] {
			value, err := strconv.ParseUint(field, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid /proc/stat value %q: %w", field, err)
			}
			values = append(values, value)
		}
		var total uint64
		for _, value := range values {
			total += value
		}
		idle := values[3]
		if len(values) > 4 {
			idle += values[4]
		}
		result[fields[0]] = cpuCounters{total: total, idle: idle}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("/proc/stat contains no CPU counters")
	}
	return result, nil
}

func readFloat(filesystem FileSystem, path string) (float64, error) {
	data, err := filesystem.ReadFile(path)
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", path, err)
	}
	return value, nil
}

func unavailable(metricID telemetry.MetricID, timestamp time.Time, reason string) telemetry.Sample {
	return telemetry.Sample{MetricID: metricID, Timestamp: timestamp, Quality: telemetry.QualityUnavailable, Error: reason}
}
