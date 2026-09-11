package testkit

import (
	"fmt"
	"sync"
)

type MSRKey struct {
	CPU      int
	Register uint32
}

type MSRWrite struct {
	MSRKey
	Value uint64
}

type MSRDevice struct {
	mu sync.Mutex

	Values map[MSRKey]uint64
	Reads  []MSRKey
	Writes []MSRWrite

	rejectWrites map[int]error
	clampWrites  map[int]uint64
	writeCount   int
}

func NewMSR(values map[MSRKey]uint64) *MSRDevice {
	device := &MSRDevice{
		Values:       make(map[MSRKey]uint64, len(values)),
		rejectWrites: make(map[int]error),
		clampWrites:  make(map[int]uint64),
	}
	for key, value := range values {
		device.Values[key] = value
	}
	return device
}

func (device *MSRDevice) Read(cpu int, register uint32) (uint64, error) {
	device.mu.Lock()
	defer device.mu.Unlock()
	key := MSRKey{CPU: cpu, Register: register}
	device.Reads = append(device.Reads, key)
	value, ok := device.Values[key]
	if !ok {
		return 0, fmt.Errorf("testkit: missing MSR CPU %d register %#x", cpu, register)
	}
	return value, nil
}

func (device *MSRDevice) Write(cpu int, register uint32, value uint64) error {
	device.mu.Lock()
	defer device.mu.Unlock()
	device.writeCount++
	key := MSRKey{CPU: cpu, Register: register}
	device.Writes = append(device.Writes, MSRWrite{MSRKey: key, Value: value})
	if err := device.rejectWrites[device.writeCount]; err != nil {
		return err
	}
	if clamped, ok := device.clampWrites[device.writeCount]; ok {
		value = clamped
	}
	device.Values[key] = value
	return nil
}

func (device *MSRDevice) RejectWrite(number int, err error) {
	device.mu.Lock()
	defer device.mu.Unlock()
	device.rejectWrites[number] = err
}

func (device *MSRDevice) ClampWrite(number int, value uint64) {
	device.mu.Lock()
	defer device.mu.Unlock()
	device.clampWrites[number] = value
}
