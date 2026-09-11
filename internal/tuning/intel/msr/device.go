package msr

import (
	"errors"
	"fmt"
)

type Device interface {
	Read(cpu int, register uint32) (uint64, error)
	Write(cpu int, register uint32, value uint64) error
}

type ReadBackMismatchError struct {
	CPU      int
	Register uint32
	Expected uint64
	Actual   uint64
}

func (err *ReadBackMismatchError) Error() string {
	return fmt.Sprintf(
		"msr: read-back mismatch on CPU %d register %#x: wrote %#x, read %#x",
		err.CPU,
		err.Register,
		err.Expected,
		err.Actual,
	)
}

func ReadModifyWrite(device Device, cpu int, register uint32, mask, value uint64) (uint64, error) {
	if !readModifyWriteRegisterAllowed(register) {
		return 0, fmt.Errorf("msr: register %#x is not allow-listed for masked writes", register)
	}
	if value&^mask != 0 {
		return 0, fmt.Errorf("msr: value %#x sets bits outside mask %#x", value, mask)
	}
	current, err := device.Read(cpu, register)
	if err != nil {
		return 0, err
	}
	requested := current&^mask | value&mask
	if err := device.Write(cpu, register, requested); err != nil {
		return 0, err
	}
	readBack, err := device.Read(cpu, register)
	if err != nil {
		return 0, err
	}
	if readBack != requested {
		return readBack, &ReadBackMismatchError{
			CPU:      cpu,
			Register: register,
			Expected: requested,
			Actual:   readBack,
		}
	}
	return readBack, nil
}

func readModifyWriteRegisterAllowed(register uint32) bool {
	switch register {
	case registerTemperatureTarget, registerTurboRatioLimit:
		return true
	default:
		return false
	}
}

const (
	ReasonDeviceMissing     = "msr_device_missing"
	ReasonPermissionDenied  = "msr_permission_denied"
	ReasonKernelLockdown    = "msr_kernel_lockdown"
	ReasonGeneralProtection = "msr_general_protection"
	ReasonReadBackMismatch  = "msr_readback_mismatch"
	ReasonIO                = "msr_io"
)

type ReasonError struct {
	Code string
	Err  error
}

func (err *ReasonError) Error() string { return fmt.Sprintf("%s: %v", err.Code, err.Err) }
func (err *ReasonError) Unwrap() error { return err.Err }

func ReasonCode(err error) string {
	var reason *ReasonError
	if errors.As(err, &reason) {
		return reason.Code
	}
	var mismatch *ReadBackMismatchError
	if errors.As(err, &mismatch) {
		return ReasonReadBackMismatch
	}
	return ""
}
