package history

import (
	"testing"

	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
)

func TestRingKeepsNewestSamplesInChronologicalOrder(t *testing.T) {
	ring := New(3)
	for value := 1; value <= 5; value++ {
		ring.Append(telemetry.Sample{Value: float64(value)})
	}
	got := ring.Snapshot()
	want := []float64{3, 4, 5}
	if len(got) != len(want) {
		t.Fatalf("samples = %d, want %d", len(got), len(want))
	}
	for index, value := range want {
		if got[index].Value != value {
			t.Fatalf("sample %d = %v, want %v", index, got[index].Value, value)
		}
	}
}

func TestRingSnapshotDoesNotExposeStorage(t *testing.T) {
	ring := New(2)
	ring.Append(telemetry.Sample{Value: 1})
	first := ring.Snapshot()
	first[0].Value = 99
	if got := ring.Snapshot()[0].Value; got != 1 {
		t.Fatalf("stored value changed to %v", got)
	}
}

func TestNewRejectsNonPositiveCapacity(t *testing.T) {
	defer func() {
		if got := recover(); got != "history: capacity must be positive" {
			t.Fatalf("panic = %v", got)
		}
	}()
	New(0)
}
