package linuxfs

import (
	"context"
	"testing"

	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
)

func TestProcStatCalculatesUtilizationFromCounterDeltas(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "proc/stat", "cpu  100 0 50 850 0 0 0 0 0 0\ncpu0 100 0 50 850 0 0 0 0 0 0\n")
	provider := NewProcStat(root)
	if _, err := provider.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}

	first, err := provider.Sample(context.Background(), []telemetry.MetricID{"cpu.utilization"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Samples[0].Quality != telemetry.QualityUnavailable || first.Samples[0].Error != "utilization requires two snapshots" {
		t.Fatalf("first sample = %+v", first.Samples[0])
	}

	writeFixture(t, root, "proc/stat", "cpu  150 0 70 880 0 0 0 0 0 0\ncpu0 150 0 70 880 0 0 0 0 0 0\n")
	second, err := provider.Sample(context.Background(), []telemetry.MetricID{"cpu.utilization"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Samples[0].Quality != telemetry.QualityGood || second.Samples[0].Value != 70 {
		t.Fatalf("second sample = %+v, want 70%%", second.Samples[0])
	}
}

func TestProcStatMarksCounterRegressionUnavailable(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "proc/stat", "cpu  100 0 50 850 0 0 0 0 0 0\n")
	provider := NewProcStat(root)
	if _, err := provider.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, _ = provider.Sample(context.Background(), []telemetry.MetricID{"cpu.utilization"})
	writeFixture(t, root, "proc/stat", "cpu  10 0 5 85 0 0 0 0 0 0\n")
	frame, err := provider.Sample(context.Background(), []telemetry.MetricID{"cpu.utilization"})
	if err != nil {
		t.Fatal(err)
	}
	if frame.Samples[0].Quality != telemetry.QualityUnavailable || frame.Samples[0].Error == "" {
		t.Fatalf("sample = %+v", frame.Samples[0])
	}
}

func TestParseProcStatDoesNotDoubleCountGuestTime(t *testing.T) {
	counters, err := parseProcStat([]byte("cpu 100 20 30 40 5 6 7 8 90 10\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := counters["cpu"].total; got != 216 {
		t.Fatalf("total = %d, want 216", got)
	}
}
