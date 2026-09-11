package msr

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
)

func DecodeTurboRatios(value uint64, count int) []float64 {
	if count < 0 {
		count = 0
	}
	if count > 8 {
		count = 8
	}
	ratios := make([]float64, count)
	for index := range ratios {
		ratios[index] = float64((value >> (index * 8)) & 0xff)
	}
	return ratios
}

func EncodeTurboRatios(current, requested []float64) (uint64, error) {
	return encodeTurboRatiosPreserving(0, current, requested)
}

func encodeTurboRatiosPreserving(raw uint64, current, requested []float64) (uint64, error) {
	if len(requested) == 0 || len(requested) != len(current) || len(requested) > 8 {
		return 0, fmt.Errorf("msr: ratio vector length must remain between 1 and 8")
	}
	encoded := raw
	for index, ratio := range requested {
		if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 1 || ratio > 255 || math.Trunc(ratio) != ratio {
			return 0, fmt.Errorf("msr: ratio %d is not a valid integer multiplier", index)
		}
		if index > 0 && ratio > requested[index-1] {
			return 0, fmt.Errorf("msr: ratio vector must be non-increasing")
		}
		if ratio > current[index] {
			return 0, fmt.Errorf("msr: ratio %d exceeds the discovered limit", index)
		}
		shift := index * 8
		encoded = encoded&^(uint64(0xff)<<shift) | uint64(ratio)<<shift
	}
	return encoded, nil
}

type ratioOperation struct {
	device    Device
	cpu       int
	register  uint32
	count     int
	requested []float64
}

func (operation *ratioOperation) ControlID() tuning.ControlID { return tuning.ControlRatioPCore }
func (operation *ratioOperation) DriverID() string            { return "intel.msr" }
func (operation *ratioOperation) Order() int                  { return tuning.OrderRatio }

func (operation *ratioOperation) Capture(ctx context.Context) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := operation.device.Read(operation.cpu, operation.register)
	if err != nil {
		return nil, err
	}
	return json.Marshal(raw)
}

func (operation *ratioOperation) Apply(ctx context.Context) (tuning.Value, error) {
	if err := ctx.Err(); err != nil {
		return tuning.Value{}, err
	}
	currentRaw, err := operation.device.Read(operation.cpu, operation.register)
	if err != nil {
		return tuning.Value{}, err
	}
	current := DecodeTurboRatios(currentRaw, operation.count)
	requestedRaw, err := encodeTurboRatiosPreserving(currentRaw, current, operation.requested)
	if err != nil {
		return tuning.Value{}, err
	}
	if err := operation.device.Write(operation.cpu, operation.register, requestedRaw); err != nil {
		return tuning.Value{}, err
	}
	readBack, err := operation.device.Read(operation.cpu, operation.register)
	if err != nil {
		return tuning.Value{}, err
	}
	if readBack != requestedRaw {
		return tuning.Value{}, &ReadBackMismatchError{CPU: operation.cpu, Register: operation.register, Expected: requestedRaw, Actual: readBack}
	}
	return tuning.VectorValue(DecodeTurboRatios(readBack, operation.count)), nil
}

func (operation *ratioOperation) Restore(ctx context.Context, raw json.RawMessage) (tuning.Value, error) {
	if err := ctx.Err(); err != nil {
		return tuning.Value{}, err
	}
	var captured uint64
	if err := json.Unmarshal(raw, &captured); err != nil {
		return tuning.Value{}, err
	}
	if err := operation.device.Write(operation.cpu, operation.register, captured); err != nil {
		return tuning.Value{}, err
	}
	readBack, err := operation.device.Read(operation.cpu, operation.register)
	if err != nil {
		return tuning.Value{}, err
	}
	if readBack != captured {
		return tuning.Value{}, &ReadBackMismatchError{CPU: operation.cpu, Register: operation.register, Expected: captured, Actual: readBack}
	}
	return tuning.VectorValue(DecodeTurboRatios(readBack, operation.count)), nil
}
