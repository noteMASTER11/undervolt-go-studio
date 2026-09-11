package linuxfs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noteMASTER11/undervolt-go-studio/internal/hardware"
	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
)

func TestHWMonDiscoversLabelsAndConvertsUnits(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "sys/class/hwmon/hwmon0/name", "coretemp\n")
	writeFixture(t, root, "sys/class/hwmon/hwmon0/temp1_label", "Package id 0\n")
	writeFixture(t, root, "sys/class/hwmon/hwmon0/temp1_input", "67000\n")
	writeFixture(t, root, "sys/class/hwmon/hwmon0/fan1_label", "CPU Fan\n")
	writeFixture(t, root, "sys/class/hwmon/hwmon0/fan1_input", "3200\n")
	provider := NewHWMon(root)

	catalog, err := provider.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Metrics) != 2 {
		t.Fatalf("metrics = %d, want 2", len(catalog.Metrics))
	}
	temperatureID := metricIDWithLabel(t, catalog, "Package id 0")
	fanID := metricIDWithLabel(t, catalog, "CPU Fan")
	frame, err := provider.Sample(context.Background(), []telemetry.MetricID{temperatureID, fanID})
	if err != nil {
		t.Fatal(err)
	}
	if frame.Samples[0].Value != 67 || frame.Samples[0].Quality != telemetry.QualityGood {
		t.Fatalf("temperature = %+v", frame.Samples[0])
	}
	if frame.Samples[1].Value != 3200 || frame.Samples[1].Quality != telemetry.QualityGood {
		t.Fatalf("fan = %+v", frame.Samples[1])
	}
}

func TestHWMonKeepsSiblingWhenSensorDisappears(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "sys/class/hwmon/hwmon0/name", "coretemp\n")
	writeFixture(t, root, "sys/class/hwmon/hwmon0/temp1_label", "Package\n")
	writeFixture(t, root, "sys/class/hwmon/hwmon0/temp1_input", "67000\n")
	writeFixture(t, root, "sys/class/hwmon/hwmon0/temp2_label", "Core 0\n")
	writeFixture(t, root, "sys/class/hwmon/hwmon0/temp2_input", "64000\n")
	provider := NewHWMon(root)
	catalog, err := provider.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "sys/class/hwmon/hwmon0/temp2_input")); err != nil {
		t.Fatal(err)
	}
	frame, err := provider.Sample(context.Background(), []telemetry.MetricID{
		metricIDWithLabel(t, catalog, "Package"),
		metricIDWithLabel(t, catalog, "Core 0"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if frame.Samples[0].Quality != telemetry.QualityGood {
		t.Fatalf("good sibling = %+v", frame.Samples[0])
	}
	if frame.Samples[1].Quality != telemetry.QualityUnavailable || frame.Samples[1].Error == "" {
		t.Fatalf("missing sensor = %+v", frame.Samples[1])
	}
}

func metricIDWithLabel(t *testing.T, catalog telemetry.Catalog, label string) telemetry.MetricID {
	t.Helper()
	for _, descriptor := range catalog.Metrics {
		if descriptor.Label == label {
			return descriptor.ID
		}
	}
	t.Fatalf("metric label %q not found in %+v", label, catalog.Metrics)
	return ""
}

func TestHWMonDiscoversSysfsSymlinkedChip(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "sys/devices/platform/coretemp/hwmon/hwmon0/name", "coretemp\n")
	writeFixture(t, root, "sys/devices/platform/coretemp/hwmon/hwmon0/temp1_input", "55000\n")
	classDirectory := filepath.Join(root, "sys/class/hwmon")
	if err := os.MkdirAll(classDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../devices/platform/coretemp/hwmon/hwmon0", filepath.Join(classDirectory, "hwmon0")); err != nil {
		t.Fatal(err)
	}

	catalog, err := NewHWMon(root).Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Metrics) != 1 {
		t.Fatalf("metrics = %d, want 1 from symlinked chip", len(catalog.Metrics))
	}
}

func TestHWMonUsesNativePathToDisambiguateSameNamedChips(t *testing.T) {
	root := t.TempDir()
	classDirectory := filepath.Join(root, "sys/class/hwmon")
	if err := os.MkdirAll(classDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	for index, address := range []string{"0000:01:00.0", "0000:02:00.0", "0000:03:00.0"} {
		target := filepath.Join(root, "sys/devices/pci0000:00", address, "hwmon", "hwmon7")
		writeFixture(t, root, filepath.ToSlash(strings.TrimPrefix(filepath.Join(target, "name"), root+string(filepath.Separator))), "samechip\n")
		writeFixture(t, root, filepath.ToSlash(strings.TrimPrefix(filepath.Join(target, "temp1_label"), root+string(filepath.Separator))), "Edge\n")
		writeFixture(t, root, filepath.ToSlash(strings.TrimPrefix(filepath.Join(target, "temp1_input"), root+string(filepath.Separator))), "50000\n")
		link := filepath.Join(classDirectory, fmt.Sprintf("hwmon%d", index))
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := NewHWMon(root).Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	deviceIDs := make(map[hardware.DeviceID]struct{})
	metricIDs := make(map[telemetry.MetricID]struct{})
	for _, device := range catalog.Devices {
		deviceIDs[device.ID] = struct{}{}
	}
	for _, descriptor := range catalog.Metrics {
		metricIDs[descriptor.ID] = struct{}{}
	}
	if len(deviceIDs) != 3 || len(metricIDs) != 3 {
		t.Fatalf("unique devices=%d metrics=%d; catalog=%+v", len(deviceIDs), len(metricIDs), catalog)
	}
}
