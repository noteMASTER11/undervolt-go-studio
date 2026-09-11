package tuning

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"time"
)

type ControlID string
type CapabilityState string
type Unit string
type ValueKind string

const (
	ControlPL1          ControlID = "intel.package.pl1"
	ControlPL2          ControlID = "intel.package.pl2"
	ControlTau          ControlID = "intel.package.tau"
	ControlEPP          ControlID = "intel.policy.epp"
	ControlThermalLimit ControlID = "intel.package.thermal_limit"
	ControlRatioPCore   ControlID = "intel.ratio.pcore"
	ControlRatioECore   ControlID = "intel.ratio.ecore"
	ControlVoltageCore  ControlID = "intel.voltage.core"
	ControlVoltageCache ControlID = "intel.voltage.cache"
)

const (
	StateSupported      CapabilityState = "supported"
	StateReadOnly       CapabilityState = "read_only"
	StateFirmwareLocked CapabilityState = "firmware_locked"
	StateKernelBlocked  CapabilityState = "kernel_blocked"
	StateRequiresProbe  CapabilityState = "requires_probe"
	StateUnavailable    CapabilityState = "unavailable"
	StateUnknownModel   CapabilityState = "unknown_model"
)

const (
	UnitWatt      Unit = "W"
	UnitSecond    Unit = "s"
	UnitCelsius   Unit = "°C"
	UnitRatio     Unit = "ratio"
	UnitMilliVolt Unit = "mV"
	UnitChoice    Unit = "choice"
)

const (
	ValueNumeric ValueKind = "numeric"
	ValueChoice  ValueKind = "choice"
	ValueVector  ValueKind = "vector"
)

const (
	OrderPower   = 10
	OrderPolicy  = 15
	OrderThermal = 20
	OrderRatio   = 30
	OrderVoltage = 40
)

var (
	ErrCrossField        = errors.New("cross-field validation failed")
	ErrStaleCapabilities = errors.New("capability generation changed")
)

type Value struct {
	Kind   ValueKind `json:"kind"`
	Number float64   `json:"number,omitempty"`
	Choice string    `json:"choice,omitempty"`
	Vector []float64 `json:"vector,omitempty"`
}

func NumericValue(number float64) Value {
	return Value{Kind: ValueNumeric, Number: number}
}

func ChoiceValue(choice string) Value {
	return Value{Kind: ValueChoice, Choice: choice}
}

func VectorValue(vector []float64) Value {
	return Value{Kind: ValueVector, Vector: slices.Clone(vector)}
}

type NumericRange struct {
	Minimum float64 `json:"minimum"`
	Maximum float64 `json:"maximum"`
	Step    float64 `json:"step"`
}

type Capability struct {
	SourceRevision    string          `json:"-"`
	ID                ControlID       `json:"id"`
	Scope             string          `json:"scope,omitempty"`
	Label             string          `json:"label,omitempty"`
	Unit              Unit            `json:"unit"`
	State             CapabilityState `json:"state"`
	Current           Value           `json:"current"`
	Range             *NumericRange   `json:"range,omitempty"`
	Choices           []string        `json:"choices,omitempty"`
	DriverID          string          `json:"driver_id,omitempty"`
	ReasonCode        string          `json:"reason_code,omitempty"`
	Reason            string          `json:"reason,omitempty"`
	RequiresPrivilege bool            `json:"requires_privilege,omitempty"`
	Experimental      bool            `json:"experimental,omitempty"`
	ObservedAt        time.Time       `json:"observed_at"`
}

func (capability Capability) Normalize(requested Value) (Value, bool, error) {
	if capability.State != StateSupported {
		return Value{}, false, fmt.Errorf("%s is %s", capability.ID, capability.State)
	}

	switch {
	case capability.Range != nil:
		return capability.normalizeNumeric(requested)
	case len(capability.Choices) > 0 || capability.Unit == UnitChoice:
		if requested.Kind != ValueChoice {
			return Value{}, false, fmt.Errorf("%s expects a choice value", capability.ID)
		}
		if !slices.Contains(capability.Choices, requested.Choice) {
			return Value{}, false, fmt.Errorf("%s does not support choice %q", capability.ID, requested.Choice)
		}
		return requested, false, nil
	case capability.Unit == UnitRatio:
		if requested.Kind != ValueVector || len(requested.Vector) == 0 {
			return Value{}, false, fmt.Errorf("%s expects a non-empty vector", capability.ID)
		}
		for _, value := range requested.Vector {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return Value{}, false, fmt.Errorf("%s contains a non-finite ratio", capability.ID)
			}
		}
		return VectorValue(requested.Vector), false, nil
	default:
		return Value{}, false, fmt.Errorf("%s has no value schema", capability.ID)
	}
}

func (capability Capability) normalizeNumeric(requested Value) (Value, bool, error) {
	if requested.Kind != ValueNumeric {
		return Value{}, false, fmt.Errorf("%s expects a numeric value", capability.ID)
	}
	if math.IsNaN(requested.Number) || math.IsInf(requested.Number, 0) {
		return Value{}, false, fmt.Errorf("%s requires a finite value", capability.ID)
	}
	if isVoltageControl(capability.ID) && requested.Number > 0 {
		return Value{}, false, fmt.Errorf("%s cannot use a positive voltage offset", capability.ID)
	}

	rangeSpec := capability.Range
	if rangeSpec.Maximum < rangeSpec.Minimum || rangeSpec.Step <= 0 {
		return Value{}, false, fmt.Errorf("%s has an invalid numeric range", capability.ID)
	}

	normalized := math.Max(rangeSpec.Minimum, math.Min(rangeSpec.Maximum, requested.Number))
	normalized = rangeSpec.Minimum + math.Round((normalized-rangeSpec.Minimum)/rangeSpec.Step)*rangeSpec.Step
	normalized = math.Max(rangeSpec.Minimum, math.Min(rangeSpec.Maximum, normalized))
	if math.Abs(normalized) < 1e-9 {
		normalized = 0
	}
	adjusted := math.Abs(normalized-requested.Number) > 1e-9
	return NumericValue(normalized), adjusted, nil
}

type CapabilitySet struct {
	Generation   string       `json:"generation"`
	MachineID    string       `json:"machine_id"`
	Capabilities []Capability `json:"capabilities"`
}

type Change struct {
	ID        ControlID `json:"id"`
	Requested Value     `json:"requested"`
}

type ChangeSet struct {
	Generation string   `json:"generation"`
	MachineID  string   `json:"machine_id"`
	Changes    []Change `json:"changes"`
}

type Adjustment struct {
	ID         ControlID `json:"id"`
	Requested  Value     `json:"requested"`
	Normalized Value     `json:"normalized"`
}

type ValidationResult struct {
	ChangeSet   ChangeSet    `json:"change_set"`
	Adjustments []Adjustment `json:"adjustments,omitempty"`
}

type Event struct {
	Time         time.Time           `json:"time"`
	Kind         string              `json:"kind"`
	Message      string              `json:"message"`
	Detail       string              `json:"detail,omitempty"`
	ControlID    ControlID           `json:"control_id,omitempty"`
	Capabilities *CapabilitySet      `json:"capabilities,omitempty"`
	Effective    map[ControlID]Value `json:"effective,omitempty"`
	Remaining    map[ControlID]Value `json:"remaining,omitempty"`
	Unverified   []ControlID         `json:"unverified,omitempty"`
}

func isVoltageControl(id ControlID) bool {
	return id == ControlVoltageCore || id == ControlVoltageCache
}

func isRatioControl(id ControlID) bool {
	return id == ControlRatioPCore || id == ControlRatioECore
}
