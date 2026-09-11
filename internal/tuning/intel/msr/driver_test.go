package msr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/intel"
)

func TestDriverUnknownModelReturnsDisabledControlsWithoutDeviceAccess(t *testing.T) {
	device := newSemanticMSRDevice(0)
	driver := NewDriver(device, intel.Identity{Vendor: "GenuineIntel", Family: 6, Model: 0xb7, Stepping: 2}, 8)

	caps, err := driver.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if device.reads != 0 || device.writes != 0 {
		t.Fatalf("unknown model touched device: reads=%d writes=%d", device.reads, device.writes)
	}
	if len(caps) != 4 {
		t.Fatalf("capabilities = %#v", caps)
	}
	for _, capability := range caps {
		if capability.State != tuning.StateUnknownModel {
			t.Fatalf("capability = %+v", capability)
		}
	}
}

func TestProductionDriverCannotAuthorizeUnprovedWrites(t *testing.T) {
	device := newSemanticMSRDevice(0x2f30313233343536)
	device.lockVoltage = true
	driver := NewDriver(device, targetIdentity(), 8)
	caps, err := driver.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []tuning.ControlID{tuning.ControlRatioPCore, tuning.ControlVoltageCore, tuning.ControlVoltageCache} {
		cap := findCapability(t, caps, id)
		if cap.State != tuning.StateReadOnly || cap.ReasonCode == "" {
			t.Errorf("unproved write advertised: %+v", cap)
		}
		value := tuning.NumericValue(-10)
		if id == tuning.ControlRatioPCore {
			value = tuning.VectorValue([]float64{53, 52, 51, 50, 49, 48, 47, 46})
		}
		if _, err := driver.Prepare(context.Background(), tuning.Change{ID: id, Requested: value}); err == nil {
			t.Errorf("unproved %s write authorized", id)
		}
	}
	if device.writes != 0 {
		t.Errorf("discovery issued %d mailbox writes despite unproved eligibility", device.writes)
	}
}

type unrestorableVoltageDevice struct{ *semanticMSRDevice }

func (device unrestorableVoltageDevice) Read(cpu int, register uint32) (uint64, error) {
	if register == registerOCMailbox {
		return uint64(1) << 21, nil
	} // +0.9765625 mV cannot be restored by this milestone.
	return device.semanticMSRDevice.Read(cpu, register)
}

func TestVoltageCaptureRejectsStockOutsideRestorePolicy(t *testing.T) {
	device := unrestorableVoltageDevice{newSemanticMSRDevice(0)}
	op := &voltageOperation{device: device, plane: PlaneCore, requested: -10}
	if _, err := op.Capture(context.Background()); err == nil {
		t.Fatal("unrestorable positive stock accepted")
	}
}

func TestRecoveryRestoreRejectsInvalidSnapshotsBeforeDeviceAccess(t *testing.T) {
	for _, id := range []tuning.ControlID{tuning.ControlVoltageCore, tuning.ControlVoltageCache, tuning.ControlRatioPCore} {
		inputs := []string{"null", "{}", "0", "1"}
		if id != tuning.ControlRatioPCore {
			inputs = []string{"null", "{}", "1", "-251", "-0.1"}
		}
		for _, raw := range inputs {
			t.Run(string(id)+"/"+raw, func(t *testing.T) {
				device := newSemanticMSRDevice(0x2f30313233343536)
				driver := NewDriver(device, targetIdentity(), 8)
				if _, err := driver.Restore(context.Background(), id, json.RawMessage(raw)); err == nil {
					t.Error("invalid snapshot accepted")
				}
				if device.reads != 0 || device.writes != 0 {
					t.Fatalf("invalid snapshot touched device: reads=%d writes=%d", device.reads, device.writes)
				}
			})
		}
	}
}

func TestRecoveryRestoreRequiresReviewedRatioTopology(t *testing.T) {
	device := newSemanticMSRDevice(0x2f30313233343536)
	driver := NewDriver(device, targetIdentity(), 4)
	raw, _ := json.Marshal(device.ratio)
	if _, err := driver.Restore(context.Background(), tuning.ControlRatioPCore, raw); err == nil || device.writes != 0 {
		t.Fatalf("unreviewed topology restored: err=%v writes=%d", err, device.writes)
	}
}

func TestRatioOperationLowersAndRestoresFullRegister(t *testing.T) {
	original := uint64(0x2f30313233343536)
	device := newSemanticMSRDevice(original)
	driver := NewDriver(device, targetIdentity(), 8)
	driver.healthInterval = 0
	if _, err := driver.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	op := &ratioOperation{device: device, register: registerTurboRatioLimit, count: 8, requested: []float64{53, 52, 51, 50, 49, 48, 47, 46}}
	raw, err := op.Capture(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := op.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if device.ratio == original {
		t.Fatal("ratio register did not change")
	}
	if _, err := op.Restore(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	if device.ratio != original {
		t.Fatalf("restored ratio = %#x, want %#x", device.ratio, original)
	}
}

func TestVoltageOperationStepsAndRestoresAfterFailure(t *testing.T) {
	device := newSemanticMSRDevice(0x2f30313233343536)
	device.failVoltageWrite = 3
	driver := NewDriver(device, targetIdentity(), 8)
	driver.healthInterval = 0
	if _, err := driver.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	op := &voltageOperation{device: device, plane: PlaneCore, requested: -35}
	if _, err := op.Capture(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := op.Apply(context.Background()); err == nil {
		t.Fatal("stepped voltage apply unexpectedly succeeded")
	}
	if math.Abs(device.offsets[PlaneCore]) > 0.001 {
		t.Fatalf("core offset was not restored: %v", device.offsets[PlaneCore])
	}
}

func TestVoltageOperationClassifiesFirmwareLock(t *testing.T) {
	device := newSemanticMSRDevice(0x2f30313233343536)
	device.lockVoltage = true
	driver := NewDriver(device, targetIdentity(), 8)
	driver.healthInterval = 0
	if _, err := driver.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	op := &voltageOperation{device: device, plane: PlaneCore, requested: -10}
	if _, err := op.Capture(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err := op.Apply(context.Background())
	var reason *ReasonError
	if !errors.As(err, &reason) || reason.Code != ReasonVoltageLocked {
		t.Fatalf("err = %v", err)
	}
}

func TestRatioReadBackMismatchRequestsRollback(t *testing.T) {
	device := newSemanticMSRDevice(0x2f30313233343536)
	driver := NewDriver(device, targetIdentity(), 8)
	if _, err := driver.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	op := &ratioOperation{device: device, register: registerTurboRatioLimit, count: 8, requested: []float64{53, 52, 51, 50, 49, 48, 47, 46}}
	device.clampRatio = true
	_, err := op.Apply(context.Background())
	var mismatch *ReadBackMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("err = %v", err)
	}
}

func TestProbeLeavesECoreRatioReadOnly(t *testing.T) {
	driver := NewDriver(newSemanticMSRDevice(0x2f30313233343536), targetIdentity(), 8)
	driver.healthInterval = 0
	caps, err := driver.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	capability := findCapability(t, caps, tuning.ControlRatioECore)
	if capability.State != tuning.StateReadOnly || capability.ReasonCode != "intel.msr.ecore_layout_unverified" {
		t.Fatalf("E-core capability = %+v", capability)
	}
}

type semanticMSRDevice struct {
	ratio uint64

	offsets          map[VoltagePlane]float64
	pendingPlane     VoltagePlane
	reads            int
	writes           int
	voltageWrites    int
	voltageSequence  []float64
	failVoltageWrite int
	lockVoltage      bool
	clampRatio       bool
}

func newSemanticMSRDevice(ratio uint64) *semanticMSRDevice {
	return &semanticMSRDevice{ratio: ratio, offsets: map[VoltagePlane]float64{PlaneCore: 0, PlaneCache: 0}}
}

func (device *semanticMSRDevice) Read(_ int, register uint32) (uint64, error) {
	device.reads++
	switch register {
	case registerTurboRatioLimit:
		return device.ratio, nil
	case registerOCMailbox:
		encoded, _, err := EncodeVoltageOffset(device.offsets[device.pendingPlane])
		return uint64(device.pendingPlane)<<40 | uint64(encoded), err
	default:
		return 0, fmt.Errorf("unexpected register %#x", register)
	}
}

func (device *semanticMSRDevice) Write(_ int, register uint32, value uint64) error {
	device.writes++
	switch register {
	case registerTurboRatioLimit:
		if !device.clampRatio {
			device.ratio = value
		}
		return nil
	case registerOCMailbox:
		device.pendingPlane = VoltagePlane((value >> 40) & 0xff)
		if value&(uint64(1)<<32) != 0 {
			device.voltageWrites++
			device.voltageSequence = append(device.voltageSequence, DecodeVoltageOffset(uint32(value)))
			if device.failVoltageWrite > 0 && device.voltageWrites == device.failVoltageWrite {
				device.failVoltageWrite = 0
				return errors.New("injected voltage failure")
			}
			if !device.lockVoltage {
				device.offsets[device.pendingPlane] = DecodeVoltageOffset(uint32(value))
			}
		}
		return nil
	default:
		return fmt.Errorf("unexpected register %#x", register)
	}
}

func targetIdentity() intel.Identity {
	return intel.Identity{Vendor: "GenuineIntel", Family: 6, Model: 0xc6, Stepping: 2}
}

func findCapability(t *testing.T, capabilities []tuning.Capability, id tuning.ControlID) tuning.Capability {
	t.Helper()
	for _, capability := range capabilities {
		if capability.ID == id {
			return capability
		}
	}
	t.Fatalf("capability %s not found", id)
	return tuning.Capability{}
}

var _ Device = (*semanticMSRDevice)(nil)
