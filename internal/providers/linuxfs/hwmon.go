package linuxfs

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/noteMASTER11/undervolt-go-studio/internal/hardware"
	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
)

const hwmonDirectory = "sys/class/hwmon"

type hwmonMetric struct {
	path    string
	divisor float64
}

type HWMon struct {
	filesystem FileSystem

	mu      sync.RWMutex
	metrics map[telemetry.MetricID]hwmonMetric
}

func NewHWMon(root string) *HWMon {
	return &HWMon{filesystem: RootFS{Root: root}, metrics: make(map[telemetry.MetricID]hwmonMetric)}
}

func (p *HWMon) ID() string {
	return "linux.hwmon"
}

func (p *HWMon) Discover(ctx context.Context) (telemetry.Catalog, error) {
	if err := ctx.Err(); err != nil {
		return telemetry.Catalog{}, err
	}
	chips, err := p.filesystem.ReadDir(hwmonDirectory)
	if err != nil {
		return telemetry.Catalog{}, err
	}
	metrics := make(map[telemetry.MetricID]hwmonMetric)
	var descriptors []telemetry.Descriptor
	var devices []hardware.Device
	for _, chipEntry := range chips {
		if err := ctx.Err(); err != nil {
			return telemetry.Catalog{}, err
		}
		chipPath := hwmonDirectory + "/" + chipEntry.Name()
		chipNameBytes, err := p.filesystem.ReadFile(chipPath + "/name")
		if err != nil {
			continue
		}
		chipName := strings.TrimSpace(string(chipNameBytes))
		kind := hardware.KindCPU
		lowerName := strings.ToLower(chipName)
		if strings.Contains(lowerName, "gpu") || strings.Contains(lowerName, "nvidia") {
			kind = hardware.KindGPU
		}
		nativeID := chipPath
		if realPath, err := p.filesystem.RealPath(chipPath); err == nil {
			nativeID = hwmonDevicePath(realPath)
		}
		deviceID := hardware.NewDeviceID(kind, chipName, nativeID)
		devices = append(devices, hardware.Device{
			ID: deviceID, Kind: kind, Vendor: chipName, Name: chipName,
			Attributes: map[string]string{"native_id": nativeID},
		})

		entries, err := p.filesystem.ReadDir(chipPath)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return telemetry.Catalog{}, err
			}
			name := entry.Name()
			metricKind, channel, divisor, unit, minimum, ok := classifyHWMonInput(name)
			if !ok {
				continue
			}
			label := channel
			if labelBytes, err := p.filesystem.ReadFile(chipPath + "/" + strings.TrimSuffix(name, "_input") + "_label"); err == nil {
				if value := strings.TrimSpace(string(labelBytes)); value != "" {
					label = value
				}
			}
			metricID := telemetry.MetricID(fmt.Sprintf(
				"hwmon.%s.%s.%s.%s.%s", slug(chipName), nativeFingerprint(nativeID), slug(label), slug(channel), metricKind,
			))
			metrics[metricID] = hwmonMetric{path: chipPath + "/" + name, divisor: divisor}
			descriptors = append(descriptors, telemetry.Descriptor{
				ID: metricID, ProviderID: p.ID(), DeviceID: deviceID, Label: label,
				Unit: telemetry.Unit(unit), MinInterval: minimum,
			})
		}
	}
	sort.Slice(descriptors, func(i, j int) bool { return descriptors[i].ID < descriptors[j].ID })
	p.mu.Lock()
	p.metrics = metrics
	p.mu.Unlock()
	return telemetry.Catalog{Devices: devices, Metrics: descriptors}, nil
}

func (p *HWMon) Sample(ctx context.Context, metricIDs []telemetry.MetricID) (telemetry.Frame, error) {
	started := time.Now()
	p.mu.RLock()
	measurements := make(map[telemetry.MetricID]hwmonMetric, len(metricIDs))
	for _, metricID := range metricIDs {
		measurements[metricID] = p.metrics[metricID]
	}
	p.mu.RUnlock()
	samples := make([]telemetry.Sample, 0, len(metricIDs))
	for _, metricID := range metricIDs {
		if err := ctx.Err(); err != nil {
			return telemetry.Frame{}, err
		}
		measurement := measurements[metricID]
		if measurement.path == "" {
			samples = append(samples, unavailable(metricID, started, "metric was not discovered"))
			continue
		}
		value, err := readFloat(p.filesystem, measurement.path)
		if err != nil {
			samples = append(samples, unavailable(metricID, started, err.Error()))
			continue
		}
		samples = append(samples, telemetry.Sample{MetricID: metricID, Value: value / measurement.divisor, Timestamp: started, Quality: telemetry.QualityGood})
	}
	return telemetry.Frame{ProviderID: p.ID(), StartedAt: started, FinishedAt: time.Now(), Samples: samples}, nil
}

func classifyHWMonInput(name string) (kind, channel string, divisor float64, unit string, minimum time.Duration, ok bool) {
	if strings.HasPrefix(name, "temp") && strings.HasSuffix(name, "_input") {
		return "temperature", strings.TrimSuffix(name, "_input"), 1000, "°C", 250 * time.Millisecond, true
	}
	if strings.HasPrefix(name, "fan") && strings.HasSuffix(name, "_input") {
		return "fan", strings.TrimSuffix(name, "_input"), 1, "RPM", time.Second, true
	}
	return "", "", 0, "", 0, false
}

func slug(value string) string {
	var builder strings.Builder
	dash := false
	for _, character := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			builder.WriteRune(character)
			dash = false
		} else if builder.Len() > 0 && !dash {
			builder.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(builder.String(), "-")
}

func hwmonDevicePath(realPath string) string {
	if index := strings.LastIndex(realPath, "/hwmon/"); index >= 0 {
		return realPath[:index]
	}
	return realPath
}

func nativeFingerprint(nativeID string) string {
	sum := sha256.Sum256([]byte(nativeID))
	return fmt.Sprintf("%x", sum[:4])
}
