package hardware

import "testing"

func TestNewDeviceIDNormalizesStableHardwareIdentity(t *testing.T) {
	a := NewDeviceID(KindCPU, " Intel ", " 0000:00:00.0 ")
	b := NewDeviceID(KindCPU, "intel", "0000:00:00.0")
	if a != b {
		t.Fatalf("normalized IDs differ: %q and %q", a, b)
	}
	if want := DeviceID("cpu:intel:0000:00:00.0"); a != want {
		t.Fatalf("ID = %q, want %q", a, want)
	}
}
