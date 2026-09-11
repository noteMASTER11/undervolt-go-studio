package tuning

import (
	"fmt"
	"math"
)

func ValidateChangeSet(capabilities CapabilitySet, changes ChangeSet) (ValidationResult, error) {
	if capabilities.Generation != changes.Generation || capabilities.MachineID != changes.MachineID {
		return ValidationResult{}, fmt.Errorf(
			"%w: expected machine %q generation %q, got machine %q generation %q",
			ErrStaleCapabilities,
			capabilities.MachineID,
			capabilities.Generation,
			changes.MachineID,
			changes.Generation,
		)
	}

	byID := make(map[ControlID]Capability, len(capabilities.Capabilities))
	for _, capability := range capabilities.Capabilities {
		byID[capability.ID] = capability
	}

	normalized := ChangeSet{
		Generation: changes.Generation,
		MachineID:  changes.MachineID,
		Changes:    make([]Change, 0, len(changes.Changes)),
	}
	result := ValidationResult{ChangeSet: normalized}
	seen := make(map[ControlID]struct{}, len(changes.Changes))

	for _, change := range changes.Changes {
		if _, duplicate := seen[change.ID]; duplicate {
			return ValidationResult{}, fmt.Errorf("duplicate control %s", change.ID)
		}
		seen[change.ID] = struct{}{}

		capability, ok := byID[change.ID]
		if !ok {
			return ValidationResult{}, fmt.Errorf("unknown control %s", change.ID)
		}
		normalizedValue, adjusted, err := capability.Normalize(change.Requested)
		if err != nil {
			return ValidationResult{}, err
		}
		if isRatioControl(change.ID) {
			if err := validateRatio(capability.Current, normalizedValue); err != nil {
				return ValidationResult{}, fmt.Errorf("%s: %w", change.ID, err)
			}
		}

		result.ChangeSet.Changes = append(result.ChangeSet.Changes, Change{ID: change.ID, Requested: normalizedValue})
		if adjusted {
			result.Adjustments = append(result.Adjustments, Adjustment{
				ID:         change.ID,
				Requested:  change.Requested,
				Normalized: normalizedValue,
			})
		}
	}

	if err := validatePowerRelationship(byID, result.ChangeSet.Changes); err != nil {
		return ValidationResult{}, err
	}
	return result, nil
}

func validateRatio(current, requested Value) error {
	if current.Kind != ValueVector {
		return fmt.Errorf("current ratio is not a vector")
	}
	if len(requested.Vector) != len(current.Vector) {
		return fmt.Errorf("ratio vector length changed from %d to %d", len(current.Vector), len(requested.Vector))
	}
	for index, value := range requested.Vector {
		if index > 0 && value > requested.Vector[index-1]+1e-9 {
			return fmt.Errorf("ratio vector must be non-increasing")
		}
		if value > current.Vector[index]+1e-9 {
			return fmt.Errorf("ratio %d exceeds the discovered limit", index)
		}
	}
	return nil
}

func validatePowerRelationship(capabilities map[ControlID]Capability, changes []Change) error {
	pl1, havePL1 := effectiveNumeric(ControlPL1, capabilities, changes)
	pl2, havePL2 := effectiveNumeric(ControlPL2, capabilities, changes)
	if havePL1 && havePL2 && pl1 > pl2+1e-9 {
		return fmt.Errorf("%w: PL1 %.3f W exceeds PL2 %.3f W", ErrCrossField, pl1, pl2)
	}
	return nil
}

func effectiveNumeric(id ControlID, capabilities map[ControlID]Capability, changes []Change) (float64, bool) {
	for _, change := range changes {
		if change.ID == id && change.Requested.Kind == ValueNumeric {
			return change.Requested.Number, true
		}
	}
	capability, ok := capabilities[id]
	if !ok || capability.Current.Kind != ValueNumeric || math.IsNaN(capability.Current.Number) {
		return 0, false
	}
	return capability.Current.Number, true
}
