package msr

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/intel"
)

type Driver struct {
	device     Device
	identity   intel.Identity
	pCoreCount int
	model      Model
	modelOK    bool

	mu             sync.RWMutex
	currentRatios  []float64
	healthInterval time.Duration
}

func NewDriver(device Device, identity intel.Identity, pCoreCount int) *Driver {
	model, ok := LookupModel(identity)
	return &Driver{
		device:         device,
		identity:       identity,
		pCoreCount:     pCoreCount,
		model:          model,
		modelOK:        ok,
		healthInterval: 250 * time.Millisecond,
	}
}

func (driver *Driver) ID() string { return "intel.msr" }

func (driver *Driver) Probe(ctx context.Context) ([]tuning.Capability, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !driver.modelOK {
		return unknownModelCapabilities(driver.identity), nil
	}

	capabilities := make([]tuning.Capability, 0, 4)
	capabilities = append(capabilities, driver.probePCoreRatios())
	capabilities = append(capabilities, tuning.Capability{
		ID:                tuning.ControlRatioECore,
		Scope:             "E-core cluster",
		Label:             "E-core turbo ratios",
		Unit:              tuning.UnitRatio,
		State:             tuning.StateReadOnly,
		DriverID:          driver.ID(),
		ReasonCode:        "intel.msr.ecore_layout_unverified",
		Reason:            "The E-core ratio register layout is not verified for this processor",
		RequiresPrivilege: true,
		Experimental:      true,
		ObservedAt:        time.Now(),
	})
	capabilities = append(capabilities, driver.probeVoltage(tuning.ControlVoltageCore, PlaneCore, "Core voltage offset"))
	capabilities = append(capabilities, driver.probeVoltage(tuning.ControlVoltageCache, PlaneCache, "Cache voltage offset"))
	return capabilities, nil
}

func (driver *Driver) probePCoreRatios() tuning.Capability {
	capability := tuning.Capability{
		ID:                tuning.ControlRatioPCore,
		Scope:             "P-core cluster",
		Label:             "P-core turbo ratios",
		Unit:              tuning.UnitRatio,
		State:             tuning.StateReadOnly,
		DriverID:          driver.ID(),
		RequiresPrivilege: true,
		Experimental:      true,
		ObservedAt:        time.Now(),
	}
	if driver.model.PCoreRatio == nil || driver.pCoreCount != driver.model.PCoreRatio.Entries {
		capability.ReasonCode = "intel.msr.pcore_topology_mismatch"
		capability.Reason = "Detected P-core count does not match the reviewed register layout"
		return capability
	}
	raw, err := driver.device.Read(0, driver.model.PCoreRatio.Register)
	if err != nil {
		capability.State = tuning.StateKernelBlocked
		capability.ReasonCode = ReasonCode(err)
		capability.Reason = "The kernel does not permit reading the reviewed ratio register"
		return capability
	}
	ratios := DecodeTurboRatios(raw, driver.pCoreCount)
	if err := validateDiscoveredRatios(ratios); err != nil {
		capability.ReasonCode = "intel.msr.invalid_ratio_table"
		capability.Reason = err.Error()
		return capability
	}
	driver.mu.Lock()
	driver.currentRatios = append([]float64(nil), ratios...)
	driver.mu.Unlock()
	capability.State = tuning.StateReadOnly
	capability.ReasonCode = "intel.msr.ratio_writability_unverified"
	capability.Reason = "Ratio writes and stock restoration cannot be verified without mutation; editing is disabled"
	capability.Current = tuning.VectorValue(ratios)
	return capability
}

func (driver *Driver) probeVoltage(id tuning.ControlID, plane VoltagePlane, label string) tuning.Capability {
	capability := tuning.Capability{
		ID:                id,
		Scope:             "CPU package",
		Label:             label,
		Unit:              tuning.UnitMilliVolt,
		State:             tuning.StateReadOnly,
		ReasonCode:        "intel.msr.voltage_writability_unverified",
		Reason:            "Voltage lock state and stock restoration cannot be established safely; editing is disabled",
		DriverID:          driver.ID(),
		RequiresPrivilege: true,
		Experimental:      true,
		ObservedAt:        time.Now(),
	}
	// The mailbox read command itself requires an MSR write. It cannot prove
	// writability, so production discovery leaves the value unknown.
	return capability
}

func (driver *Driver) Prepare(ctx context.Context, change tuning.Change) (tuning.PreparedOperation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !driver.modelOK {
		return nil, fmt.Errorf("msr: processor model is not allow-listed")
	}
	return nil, fmt.Errorf("msr: writability and restoration eligibility are unverified for %s", change.ID)
}

func (driver *Driver) Restore(ctx context.Context, id tuning.ControlID, raw json.RawMessage) (tuning.Value, error) {
	if !driver.modelOK {
		return tuning.Value{}, fmt.Errorf("msr: processor model is not allow-listed")
	}
	switch id {
	case tuning.ControlRatioPCore:
		if driver.model.PCoreRatio == nil || driver.pCoreCount != driver.model.PCoreRatio.Entries {
			return tuning.Value{}, fmt.Errorf("msr: ratio recovery topology does not match the reviewed layout")
		}
		return (&ratioOperation{device: driver.device, cpu: 0, register: driver.model.PCoreRatio.Register, count: driver.pCoreCount}).Restore(ctx, raw)
	case tuning.ControlVoltageCore, tuning.ControlVoltageCache:
		plane := PlaneCore
		if id == tuning.ControlVoltageCache {
			plane = PlaneCache
		}
		return (&voltageOperation{device: driver.device, cpu: 0, plane: plane, id: id, healthInterval: driver.healthInterval}).Restore(ctx, raw)
	default:
		return tuning.Value{}, fmt.Errorf("msr: unsupported restore control %s", id)
	}
}

func validateDiscoveredRatios(ratios []float64) error {
	for index, ratio := range ratios {
		if ratio <= 0 {
			return fmt.Errorf("ratio %d is zero", index)
		}
		if index > 0 && ratio > ratios[index-1] {
			return fmt.Errorf("ratio table is not non-increasing")
		}
	}
	return nil
}

func unknownModelCapabilities(identity intel.Identity) []tuning.Capability {
	ids := []struct {
		id    tuning.ControlID
		label string
		unit  tuning.Unit
	}{
		{tuning.ControlRatioPCore, "P-core turbo ratios", tuning.UnitRatio},
		{tuning.ControlRatioECore, "E-core turbo ratios", tuning.UnitRatio},
		{tuning.ControlVoltageCore, "Core voltage offset", tuning.UnitMilliVolt},
		{tuning.ControlVoltageCache, "Cache voltage offset", tuning.UnitMilliVolt},
	}
	capabilities := make([]tuning.Capability, 0, len(ids))
	for _, item := range ids {
		capabilities = append(capabilities, tuning.Capability{
			ID:           item.id,
			Label:        item.label,
			Unit:         item.unit,
			State:        tuning.StateUnknownModel,
			DriverID:     "intel.msr",
			ReasonCode:   "intel.msr.model_not_allowlisted",
			Reason:       fmt.Sprintf("CPU %s family %d model %#x stepping %d is not allow-listed", identity.Vendor, identity.Family, identity.Model, identity.Stepping),
			Experimental: true,
			ObservedAt:   time.Now(),
		})
	}
	return capabilities
}
