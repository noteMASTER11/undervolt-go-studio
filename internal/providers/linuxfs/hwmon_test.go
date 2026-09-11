package linuxfs

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
	temperatureID := telemetry.MetricID("hwmon.coretemp.package-id-0.temperature")
	fanID := telemetry.MetricID("hwmon.coretemp.cpu-fan.fan")
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
	if _, err := provider.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "sys/class/hwmon/hwmon0/temp2_input")); err != nil {
		t.Fatal(err)
	}
	frame, err := provider.Sample(context.Background(), []telemetry.MetricID{
		"hwmon.coretemp.package.temperature",
		"hwmon.coretemp.core-0.temperature",
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
