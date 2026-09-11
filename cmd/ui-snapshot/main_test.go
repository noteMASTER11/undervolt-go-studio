package main

import (
	"context"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
)

func TestIntel275HXSnapshotServiceReportsRequiredCapabilitiesHonestly(t *testing.T) {
	results := intel275HXSnapshotService{}.Discover(context.Background())
	result, ok := <-results
	if !ok {
		t.Fatal("snapshot discovery closed without a result")
	}
	if !result.Complete {
		t.Fatal("snapshot discovery did not complete")
	}

	assertIntel275HXSnapshotCapabilities(t, result.Set.Capabilities)
}

func TestIntel275HXSnapshotPageWaitsForRenderedCompletedDiscovery(t *testing.T) {
	snapshot, err := snapshotPage("tune", "intel-275hx")
	if err != nil {
		t.Fatalf("create Intel 275HX snapshot page: %v", err)
	}
	defer snapshot.deactivate()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := snapshot.wait(ctx); err != nil {
		t.Fatalf("wait for rendered Tune discovery: %v", err)
	}
	if snapshot.tune == nil {
		t.Fatal("Intel 275HX snapshot did not expose its Tune state")
	}
	state := snapshot.tune.State()
	if got, want := state.Phase, "idle"; got != want {
		t.Fatalf("Tune phase = %q, want %q", got, want)
	}
	if got, want := state.Capabilities.Generation, "intel-275hx-snapshot-v1"; got != want {
		t.Fatalf("Tune capability generation = %q, want %q", got, want)
	}
	assertIntel275HXSnapshotCapabilities(t, state.Capabilities.Capabilities)
}

func assertIntel275HXSnapshotCapabilities(t *testing.T, reported []tuning.Capability) {
	t.Helper()
	capabilities := make(map[tuning.ControlID]tuning.Capability, len(reported))
	for _, capability := range reported {
		capabilities[capability.ID] = capability
	}
	assertSnapshotNumericCapability(t, capabilities, tuning.ControlPL1, 44, tuning.UnitWatt, tuning.StateSupported)
	assertSnapshotNumericCapability(t, capabilities, tuning.ControlPL2, 44, tuning.UnitWatt, tuning.StateSupported)
	assertSnapshotNumericCapability(t, capabilities, tuning.ControlThermalLimit, 95, tuning.UnitCelsius, tuning.StateSupported)

	epp, ok := capabilities[tuning.ControlEPP]
	if !ok {
		t.Fatal("EPP capability missing")
	}
	if got, want := epp.Current, tuning.ChoiceValue("balanced"); !reflect.DeepEqual(got, want) {
		t.Fatalf("EPP current = %#v, want %#v", got, want)
	}
	if got, want := epp.Choices, []string{"performance", "balanced", "power saver"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("EPP choices = %v, want %v", got, want)
	}

	ratio, ok := capabilities[tuning.ControlRatioPCore]
	if !ok {
		t.Fatal("P-core ratio capability missing")
	}
	if ratio.State != tuning.StateSupported || ratio.Unit != tuning.UnitRatio {
		t.Fatalf("P-core ratio capability = %#v", ratio)
	}
	if got, want := ratio.Current.Vector, []float64{50, 50, 49, 49, 48, 48, 47, 47}; !reflect.DeepEqual(got, want) {
		t.Fatalf("P-core ratios = %v, want %v", got, want)
	}

	voltage, ok := capabilities[tuning.ControlVoltageCore]
	if !ok {
		t.Fatal("core voltage capability missing")
	}
	if voltage.State != tuning.StateFirmwareLocked {
		t.Fatalf("core voltage state = %q, want %q", voltage.State, tuning.StateFirmwareLocked)
	}
	if !reflect.DeepEqual(voltage.Current, tuning.Value{}) {
		t.Fatalf("locked voltage current = %#v, want unset", voltage.Current)
	}
	if voltage.Range != nil {
		t.Fatalf("locked voltage range = %#v, want nil", voltage.Range)
	}
}

func assertSnapshotNumericCapability(t *testing.T, capabilities map[tuning.ControlID]tuning.Capability, id tuning.ControlID, value float64, unit tuning.Unit, state tuning.CapabilityState) {
	t.Helper()
	capability, ok := capabilities[id]
	if !ok {
		t.Fatalf("%s capability missing", id)
	}
	if got, want := capability.Current, tuning.NumericValue(value); !reflect.DeepEqual(got, want) {
		t.Fatalf("%s current = %#v, want %#v", id, got, want)
	}
	if capability.Unit != unit || capability.State != state {
		t.Fatalf("%s capability unit/state = %q/%q, want %q/%q", id, capability.Unit, capability.State, unit, state)
	}
}

func TestIntel275HXScenarioRendersTuneSnapshot(t *testing.T) {
	output := filepath.Join(t.TempDir(), "tune-275hx.png")
	command := exec.Command("go", "run", ".",
		"--page", "tune",
		"--scenario", "intel-275hx",
		"--output", output,
		"--width", "800",
		"--height", "600",
	)
	if result, err := command.CombinedOutput(); err != nil {
		t.Fatalf("render Intel 275HX Tune snapshot: %v\n%s", err, result)
	}

	file, err := os.Open(output)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer file.Close()
	image, err := png.Decode(file)
	if err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if got, want := image.Bounds().Dx(), 800; got != want {
		t.Fatalf("snapshot width = %d, want %d", got, want)
	}
	if got, want := image.Bounds().Dy(), 600; got != want {
		t.Fatalf("snapshot height = %d, want %d", got, want)
	}
}
