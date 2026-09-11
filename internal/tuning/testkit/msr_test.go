package testkit

import (
	"errors"
	"testing"
)

func TestMSRDeviceCanRejectAndClampWrites(t *testing.T) {
	key := MSRKey{CPU: 0, Register: 0x150}
	device := NewMSR(map[MSRKey]uint64{key: 1})
	device.ClampWrite(1, 2)
	if err := device.Write(key.CPU, key.Register, 9); err != nil {
		t.Fatal(err)
	}
	if got, _ := device.Read(key.CPU, key.Register); got != 2 {
		t.Fatalf("clamped value = %d", got)
	}
	want := errors.New("rejected")
	device.RejectWrite(2, want)
	if err := device.Write(key.CPU, key.Register, 10); !errors.Is(err, want) {
		t.Fatalf("err = %v", err)
	}
}
