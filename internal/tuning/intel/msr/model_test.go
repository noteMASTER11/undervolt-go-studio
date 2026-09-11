package msr

import (
	"errors"
	"testing"

	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/intel"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/testkit"
)

func TestLookupModelAllowsOnlyC6Stepping2(t *testing.T) {
	identity := intel.Identity{Vendor: "GenuineIntel", Family: 6, Model: 0xc6, Stepping: 2}
	model, ok := LookupModel(identity)
	if !ok {
		t.Fatal("target rejected")
	}
	if model.PCoreRatio == nil || model.ECoreRatio != nil || model.Voltage == nil {
		t.Fatalf("unsafe or incomplete model layout: %+v", model)
	}
	for _, rejected := range []intel.Identity{
		{Vendor: "AuthenticAMD", Family: 6, Model: 0xc6, Stepping: 2},
		{Vendor: "GenuineIntel", Family: 6, Model: 0xb7, Stepping: 2},
		{Vendor: "GenuineIntel", Family: 6, Model: 0xc6, Stepping: 1},
	} {
		if _, ok := LookupModel(rejected); ok {
			t.Fatalf("unexpected model accepted: %+v", rejected)
		}
	}
}

func TestReadModifyWritePreservesBitsOutsideMask(t *testing.T) {
	original := uint64(0xa5a5a5a5a5a5a5a5)
	mask := uint64(0x7f) << 24
	device := testkit.NewMSR(map[testkit.MSRKey]uint64{{CPU: 0, Register: 0x1a2}: original})

	got, err := ReadModifyWrite(device, 0, 0x1a2, mask, uint64(10)<<24)
	if err != nil {
		t.Fatal(err)
	}
	if got&^mask != original&^mask {
		t.Fatal("reserved bits changed")
	}
	if got&mask != uint64(10)<<24 {
		t.Fatalf("masked bits = %#x", got&mask)
	}
	if len(device.Reads) != 2 || len(device.Writes) != 1 {
		t.Fatalf("reads=%#v writes=%#v", device.Reads, device.Writes)
	}
}

func TestReadModifyWriteReportsClampedReadBack(t *testing.T) {
	device := testkit.NewMSR(map[testkit.MSRKey]uint64{{CPU: 0, Register: 0x1a2}: 0})
	device.ClampWrite(1, uint64(9)<<24)

	got, err := ReadModifyWrite(device, 0, 0x1a2, uint64(0x7f)<<24, uint64(10)<<24)
	var mismatch *ReadBackMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("err = %v", err)
	}
	if got != uint64(9)<<24 || mismatch.Actual != got {
		t.Fatalf("got=%#x mismatch=%+v", got, mismatch)
	}
}

func TestReadModifyWriteDoesNotWriteOutsideAllowList(t *testing.T) {
	device := testkit.NewMSR(map[testkit.MSRKey]uint64{{CPU: 0, Register: 0xdead}: 1})
	if _, err := ReadModifyWrite(device, 0, 0xdead, 1, 0); err == nil {
		t.Fatal("unknown register accepted")
	}
	if len(device.Writes) != 0 {
		t.Fatalf("writes = %#v", device.Writes)
	}
}
