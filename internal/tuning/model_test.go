package tuning

import (
	"math"
	"testing"
)

func TestNormalizeNumericClampsToRepresentableStep(t *testing.T) {
	capability := Capability{
		ID:    ControlPL1,
		Unit:  UnitWatt,
		State: StateSupported,
		Range: &NumericRange{Minimum: 15, Maximum: 55, Step: 0.125},
	}

	got, adjusted, err := capability.Normalize(NumericValue(44.06))
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got.Number-44) > 1e-9 || !adjusted {
		t.Fatalf("got %+v adjusted=%v", got, adjusted)
	}
}

func TestNormalizeRejectsPositiveVoltage(t *testing.T) {
	capability := Capability{
		ID:    ControlVoltageCore,
		Unit:  UnitMilliVolt,
		State: StateSupported,
		Range: &NumericRange{Minimum: -250, Maximum: 0, Step: 0.9765625},
	}

	if _, _, err := capability.Normalize(NumericValue(10)); err == nil {
		t.Fatal("positive voltage offset accepted")
	}
}

func TestNormalizeClampsNumericValueToRange(t *testing.T) {
	capability := Capability{
		ID:    ControlPL1,
		Unit:  UnitWatt,
		State: StateSupported,
		Range: &NumericRange{Minimum: 15, Maximum: 55, Step: 1},
	}

	got, adjusted, err := capability.Normalize(NumericValue(70))
	if err != nil {
		t.Fatal(err)
	}
	if got.Number != 55 || !adjusted {
		t.Fatalf("got %+v adjusted=%v", got, adjusted)
	}
}

func TestNormalizeRejectsWrongValueKind(t *testing.T) {
	capability := Capability{
		ID:      ControlEPP,
		Unit:    UnitChoice,
		State:   StateSupported,
		Choices: []string{"performance", "balance_performance"},
	}

	if _, _, err := capability.Normalize(NumericValue(1)); err == nil {
		t.Fatal("numeric value accepted for a choice capability")
	}
}
