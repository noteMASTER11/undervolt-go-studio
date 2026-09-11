package viewmodel

import (
	"testing"
	"time"

	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
)

func TestOverviewUsesOnlyAvailableSummaryMetrics(t *testing.T) {
	source := &fakeSource{}
	catalog := telemetry.Catalog{Metrics: []telemetry.Descriptor{
		{ID: "cpu.utilization", Label: "CPU Utilization", Unit: "%"},
		{ID: "hwmon.coretemp.package.temperature", Label: "Package", Unit: "°C"},
		{ID: "cpu.0.frequency", Label: "CPU 0 Frequency", Unit: "MHz"},
		{ID: "hwmon.nct.fan1.fan", Label: "Fan 1", Unit: "RPM"},
		{ID: "gpu.temperature", Label: "GPU Temperature", Unit: "°C"},
	}}
	vm := NewOverview(source, catalog, 250*time.Millisecond)
	vm.Activate()
	vm.Deactivate()

	if len(source.subscriptions) != 1 {
		t.Fatalf("subscriptions = %d", len(source.subscriptions))
	}
	got := source.subscriptions[0].MetricIDs
	want := []telemetry.MetricID{
		"cpu.utilization",
		"hwmon.coretemp.package.temperature",
		"cpu.0.frequency",
		"hwmon.nct.fan1.fan",
	}
	if len(got) != len(want) {
		t.Fatalf("metric IDs = %v", got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("metric IDs = %v, want %v", got, want)
		}
	}
}
