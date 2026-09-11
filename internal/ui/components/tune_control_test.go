package components

import (
	"strings"
	"testing"

	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
)

func TestTuneControlExplainsLockedCapability(t *testing.T) {
	control := NewTuneControl(tuning.Capability{
		ID: tuning.ControlVoltageCore, Label: "Core voltage offset", State: tuning.StateFirmwareLocked,
		Reason: "BIOS / undervolt protection blocks writes",
	}, nil)
	if control.Enabled() {
		t.Fatal("locked control enabled")
	}
	if !strings.Contains(control.StatusText(), "BIOS") {
		t.Fatalf("status = %q", control.StatusText())
	}
}

func TestTuneControlEnablesSupportedNumericCapability(t *testing.T) {
	control := NewTuneControl(tuning.Capability{
		ID: tuning.ControlPL1, Label: "Sustained power", Unit: tuning.UnitWatt, State: tuning.StateSupported,
		Current: tuning.NumericValue(44), Range: &tuning.NumericRange{Minimum: 15, Maximum: 55, Step: 1},
	}, func(tuning.ControlID, tuning.Value) {})
	if !control.Enabled() || control.Object() == nil {
		t.Fatalf("control enabled=%v object=%v", control.Enabled(), control.Object())
	}
}

func TestTuneControlChoiceConstructionDoesNotStageCurrentValue(t *testing.T) {
	staged := 0
	NewTuneControl(tuning.Capability{
		ID: tuning.ControlEPP, Label: "Energy preference", Unit: tuning.UnitChoice, State: tuning.StateSupported,
		Current: tuning.ChoiceValue("balance_performance"), Choices: []string{"performance", "balance_performance"},
	}, func(tuning.ControlID, tuning.Value) { staged++ })
	if staged != 0 {
		t.Fatalf("construction staged %d changes", staged)
	}
}
