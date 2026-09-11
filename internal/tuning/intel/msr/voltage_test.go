package msr

import (
	"context"
	"math"
	"testing"
)

func TestVoltageTransitionsNeverExceedTenMailboxUnits(t *testing.T) {
	for _, target := range []float64{-20, -30, -50, -125, -250} {
		device := newSemanticMSRDevice(0)
		op := &voltageOperation{device: device, plane: PlaneCore, requested: target}
		if _, err := op.Capture(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := op.Apply(context.Background()); err != nil {
			t.Fatal(err)
		}
		last := 0.0
		for _, value := range device.voltageSequence {
			if math.Abs(value-last) > 9.765625 {
				t.Fatalf("target %v: transition %.9f -> %.9f exceeds ten mailbox units", target, last, value)
			}
			last = value
		}
	}
}

func TestVoltageOffsetRoundTrip(t *testing.T) {
	for _, millivolts := range []float64{0, -10, -50, -125} {
		encoded, normalized, err := EncodeVoltageOffset(millivolts)
		if err != nil {
			t.Fatal(err)
		}
		if math.Abs(DecodeVoltageOffset(encoded)-normalized) > 0.001 {
			t.Fatalf("round trip %v", millivolts)
		}
	}
}

func TestVoltageOffsetRejectsUnsafeRange(t *testing.T) {
	for _, millivolts := range []float64{1, -250.1, math.NaN()} {
		if _, _, err := EncodeVoltageOffset(millivolts); err == nil {
			t.Fatalf("unsafe offset %v accepted", millivolts)
		}
	}
}

func TestMailboxCommandsAreFixedByPlane(t *testing.T) {
	if got := PackVoltageRead(PlaneCore); got != 0x8000001000000000 {
		t.Fatalf("read = %#x", got)
	}
	if got := PackVoltageWrite(PlaneCore, 0); got != 0x8000001100000000 {
		t.Fatalf("write = %#x", got)
	}
	if got := PackVoltageRead(VoltagePlane(99)); got != 0 {
		t.Fatalf("unknown plane command = %#x", got)
	}
}
