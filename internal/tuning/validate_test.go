package tuning

import (
	"errors"
	"reflect"
	"testing"
)

func TestValidateChangeSetRejectsPL1AbovePL2(t *testing.T) {
	caps := CapabilitySet{Generation: "g1", MachineID: "cpu", Capabilities: []Capability{
		{ID: ControlPL1, State: StateSupported, Current: NumericValue(44), Range: &NumericRange{Minimum: 15, Maximum: 55, Step: 1}},
		{ID: ControlPL2, State: StateSupported, Current: NumericValue(45), Range: &NumericRange{Minimum: 15, Maximum: 160, Step: 1}},
	}}

	_, err := ValidateChangeSet(caps, ChangeSet{
		Generation: "g1",
		MachineID:  "cpu",
		Changes:    []Change{{ID: ControlPL1, Requested: NumericValue(50)}},
	})
	if !errors.Is(err, ErrCrossField) {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateChangeSetRejectsStaleGeneration(t *testing.T) {
	_, err := ValidateChangeSet(
		CapabilitySet{Generation: "new", MachineID: "cpu"},
		ChangeSet{Generation: "old", MachineID: "cpu"},
	)
	if !errors.Is(err, ErrStaleCapabilities) {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateChangeSetReturnsNormalizedChangesAndAdjustments(t *testing.T) {
	caps := CapabilitySet{Generation: "g1", MachineID: "cpu", Capabilities: []Capability{
		{ID: ControlPL1, State: StateSupported, Current: NumericValue(40), Range: &NumericRange{Minimum: 15, Maximum: 55, Step: 0.125}},
		{ID: ControlPL2, State: StateSupported, Current: NumericValue(55), Range: &NumericRange{Minimum: 15, Maximum: 160, Step: 0.125}},
	}}

	result, err := ValidateChangeSet(caps, ChangeSet{
		Generation: "g1",
		MachineID:  "cpu",
		Changes:    []Change{{ID: ControlPL1, Requested: NumericValue(44.06)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.ChangeSet.Changes[0].Requested.Number; got != 44 {
		t.Fatalf("normalized PL1 = %v", got)
	}
	want := []Adjustment{{ID: ControlPL1, Requested: NumericValue(44.06), Normalized: NumericValue(44)}}
	if !reflect.DeepEqual(result.Adjustments, want) {
		t.Fatalf("adjustments = %#v, want %#v", result.Adjustments, want)
	}
}

func TestValidateChangeSetRejectsDuplicateControls(t *testing.T) {
	caps := CapabilitySet{Generation: "g1", MachineID: "cpu", Capabilities: []Capability{
		{ID: ControlPL1, State: StateSupported, Current: NumericValue(40), Range: &NumericRange{Minimum: 15, Maximum: 55, Step: 1}},
	}}
	set := ChangeSet{Generation: "g1", MachineID: "cpu", Changes: []Change{
		{ID: ControlPL1, Requested: NumericValue(35)},
		{ID: ControlPL1, Requested: NumericValue(30)},
	}}

	if _, err := ValidateChangeSet(caps, set); err == nil {
		t.Fatal("duplicate control accepted")
	}
}

func TestValidateChangeSetRejectsRatioIncrease(t *testing.T) {
	caps := CapabilitySet{Generation: "g1", MachineID: "cpu", Capabilities: []Capability{
		{ID: ControlRatioPCore, Unit: UnitRatio, State: StateSupported, Current: VectorValue([]float64{57, 57, 54})},
	}}
	set := ChangeSet{Generation: "g1", MachineID: "cpu", Changes: []Change{
		{ID: ControlRatioPCore, Requested: VectorValue([]float64{58, 57, 54})},
	}}

	if _, err := ValidateChangeSet(caps, set); err == nil {
		t.Fatal("ratio increase beyond current limits accepted")
	}
}

func TestValidateChangeSetRejectsNonMonotonicRatioVector(t *testing.T) {
	caps := CapabilitySet{Generation: "g1", MachineID: "cpu", Capabilities: []Capability{
		{ID: ControlRatioPCore, Unit: UnitRatio, State: StateSupported, Current: VectorValue([]float64{57, 57, 54})},
	}}
	set := ChangeSet{Generation: "g1", MachineID: "cpu", Changes: []Change{
		{ID: ControlRatioPCore, Requested: VectorValue([]float64{54, 55, 53})},
	}}

	if _, err := ValidateChangeSet(caps, set); err == nil {
		t.Fatal("non-monotonic ratio vector accepted")
	}
}
