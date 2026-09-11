package msr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
)

const ReasonVoltageLocked = "intel.msr.voltage_locked"

func EncodeVoltageOffset(millivolts float64) (uint32, float64, error) {
	if math.IsNaN(millivolts) || math.IsInf(millivolts, 0) || millivolts > 0 || millivolts < -250 {
		return 0, 0, fmt.Errorf("msr: voltage offset must be finite and between -250 and 0 mV")
	}
	rounded := int(math.Round(millivolts * 1.024))
	encoded := uint32(rounded&0x7ff) << 21
	normalized := float64(rounded) / 1.024
	if math.Abs(normalized) < 1e-9 {
		normalized = 0
	}
	return encoded, normalized, nil
}

func DecodeVoltageOffset(encoded uint32) float64 {
	raw := int((encoded >> 21) & 0x7ff)
	if raw > 1023 {
		raw -= 2048
	}
	return float64(raw) / 1.024
}

func PackVoltageRead(plane VoltagePlane) uint64 {
	if !validVoltagePlane(plane) {
		return 0
	}
	return uint64(1)<<63 | uint64(plane)<<40 | uint64(1)<<36
}

func PackVoltageWrite(plane VoltagePlane, encoded uint32) uint64 {
	if !validVoltagePlane(plane) || encoded&0x1fffff != 0 {
		return 0
	}
	return PackVoltageRead(plane) | uint64(1)<<32 | uint64(encoded)
}

func validVoltagePlane(plane VoltagePlane) bool {
	return plane == PlaneCore || plane == PlaneCache
}

func readVoltage(device Device, cpu int, plane VoltagePlane) (float64, error) {
	command := PackVoltageRead(plane)
	if command == 0 {
		return 0, fmt.Errorf("msr: unknown voltage plane %d", plane)
	}
	if err := device.Write(cpu, registerOCMailbox, command); err != nil {
		return 0, err
	}
	response, err := device.Read(cpu, registerOCMailbox)
	if err != nil {
		return 0, err
	}
	if response&(uint64(1)<<63) != 0 {
		return 0, fmt.Errorf("msr: voltage mailbox remained busy")
	}
	if VoltagePlane((response>>40)&0xff) != plane {
		return 0, fmt.Errorf("msr: voltage mailbox returned a different plane")
	}
	return DecodeVoltageOffset(uint32(response)), nil
}

func writeVoltage(device Device, cpu int, plane VoltagePlane, millivolts float64) (float64, error) {
	encoded, normalized, err := EncodeVoltageOffset(millivolts)
	if err != nil {
		return 0, err
	}
	command := PackVoltageWrite(plane, encoded)
	if command == 0 {
		return 0, fmt.Errorf("msr: could not construct voltage command")
	}
	if err := device.Write(cpu, registerOCMailbox, command); err != nil {
		return 0, err
	}
	readBack, err := readVoltage(device, cpu, plane)
	if err != nil {
		return 0, err
	}
	if normalized != 0 && math.Abs(readBack) < 0.001 {
		return readBack, &ReasonError{Code: ReasonVoltageLocked, Err: fmt.Errorf("firmware returned zero for %.3f mV", normalized)}
	}
	if math.Abs(readBack-normalized) > 0.001 {
		return readBack, &ReadBackMismatchError{CPU: cpu, Register: registerOCMailbox, Expected: uint64(encoded), Actual: uint64(uint32(math.Round(readBack*1.024))) << 21}
	}
	return readBack, nil
}

type voltageOperation struct {
	device         Device
	cpu            int
	plane          VoltagePlane
	id             tuning.ControlID
	requested      float64
	healthInterval time.Duration
	captured       *float64
}

func (operation *voltageOperation) ControlID() tuning.ControlID { return operation.id }
func (operation *voltageOperation) DriverID() string            { return "intel.msr" }
func (operation *voltageOperation) Order() int                  { return tuning.OrderVoltage }

func (operation *voltageOperation) Capture(ctx context.Context) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	current, err := readVoltage(operation.device, operation.cpu, operation.plane)
	if err != nil {
		return nil, err
	}
	operation.captured = &current
	return json.Marshal(current)
}

func (operation *voltageOperation) Apply(ctx context.Context) (tuning.Value, error) {
	if operation.captured == nil {
		return tuning.Value{}, fmt.Errorf("msr: voltage operation must be captured before apply")
	}
	effective, err := operation.stepTo(ctx, operation.requested)
	if err == nil {
		return tuning.NumericValue(effective), nil
	}
	rollbackContext := context.WithoutCancel(ctx)
	_, rollbackErr := operation.stepTo(rollbackContext, *operation.captured)
	return tuning.Value{}, errors.Join(err, rollbackErr)
}

func (operation *voltageOperation) Restore(ctx context.Context, raw json.RawMessage) (tuning.Value, error) {
	var captured float64
	if err := json.Unmarshal(raw, &captured); err != nil {
		return tuning.Value{}, err
	}
	effective, err := operation.stepTo(ctx, captured)
	return tuning.NumericValue(effective), err
}

func (operation *voltageOperation) stepTo(ctx context.Context, target float64) (float64, error) {
	current, err := readVoltage(operation.device, operation.cpu, operation.plane)
	if err != nil {
		return 0, err
	}
	delta := target - current
	steps := int(math.Ceil(math.Abs(delta) / 10))
	if steps == 0 {
		return current, nil
	}
	effective := current
	for step := 1; step <= steps; step++ {
		if err := ctx.Err(); err != nil {
			return effective, err
		}
		next := current + delta*float64(step)/float64(steps)
		effective, err = writeVoltage(operation.device, operation.cpu, operation.plane, next)
		if err != nil {
			return effective, err
		}
		if err := waitForHealth(ctx, operation.healthInterval); err != nil {
			return effective, err
		}
	}
	return effective, nil
}

func waitForHealth(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
