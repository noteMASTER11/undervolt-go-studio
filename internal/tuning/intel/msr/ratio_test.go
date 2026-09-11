package msr

import (
	"reflect"
	"testing"
)

func TestDecodeTurboRatiosUsesOneBytePerActiveCoreCount(t *testing.T) {
	got := DecodeTurboRatios(0x2f30313233343536, 8)
	want := []float64{54, 53, 52, 51, 50, 49, 48, 47}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestEncodeTurboRatiosRejectsIncreaseAndNonMonotonicVector(t *testing.T) {
	current := []float64{54, 53, 52, 52, 51, 51, 50, 50}
	if _, err := EncodeTurboRatios(current, []float64{55, 53, 52, 52, 51, 51, 50, 50}); err == nil {
		t.Fatal("increase accepted")
	}
	if _, err := EncodeTurboRatios(current, []float64{54, 53, 52, 53, 51, 51, 50, 50}); err == nil {
		t.Fatal("non-monotonic vector accepted")
	}
}

func TestEncodeTurboRatiosPreservesUnusedBytes(t *testing.T) {
	currentRaw := uint64(0xaabbccdd33343536)
	current := DecodeTurboRatios(currentRaw, 4)
	got, err := encodeTurboRatiosPreserving(currentRaw, current, []float64{53, 52, 51, 50})
	if err != nil {
		t.Fatal(err)
	}
	if got>>32 != currentRaw>>32 {
		t.Fatalf("unused bytes changed: got %#x, current %#x", got, currentRaw)
	}
}
