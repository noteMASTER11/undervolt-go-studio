package linuxfs

import (
	"context"
	"testing"

	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
)

func TestCPUFreqDiscoversNumericOrderAndSamplesMHz(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "sys/devices/system/cpu/cpu10/cpufreq/scaling_cur_freq", "3100000\n")
	writeFixture(t, root, "sys/devices/system/cpu/cpu2/cpufreq/scaling_cur_freq", "4200000\n")
	provider := NewCPUFreq(root)

	catalog, err := provider.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Metrics) != 2 {
		t.Fatalf("metrics = %d, want 2", len(catalog.Metrics))
	}
	if catalog.Metrics[0].ID != "cpu.2.frequency" || catalog.Metrics[1].ID != "cpu.10.frequency" {
		t.Fatalf("metric order = [%s %s]", catalog.Metrics[0].ID, catalog.Metrics[1].ID)
	}

	frame, err := provider.Sample(context.Background(), []telemetry.MetricID{"cpu.2.frequency", "cpu.10.frequency"})
	if err != nil {
		t.Fatal(err)
	}
	if frame.Samples[0].Value != 4200 || frame.Samples[1].Value != 3100 {
		t.Fatalf("MHz = [%v %v]", frame.Samples[0].Value, frame.Samples[1].Value)
	}
}

func TestCPUFreqMarksMalformedMetricUnavailable(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "sys/devices/system/cpu/cpu0/cpufreq/scaling_cur_freq", "not-a-number\n")
	provider := NewCPUFreq(root)
	if _, err := provider.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	frame, err := provider.Sample(context.Background(), []telemetry.MetricID{"cpu.0.frequency"})
	if err != nil {
		t.Fatal(err)
	}
	if frame.Samples[0].Quality != telemetry.QualityUnavailable || frame.Samples[0].Error == "" {
		t.Fatalf("sample = %+v", frame.Samples[0])
	}
}
