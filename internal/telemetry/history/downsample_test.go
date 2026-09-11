package history

import (
	"testing"
	"time"

	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
)

func TestDownsampleMinMaxPreservesSpikeAndChronology(t *testing.T) {
	samples := make([]telemetry.Sample, 100)
	for index := range samples {
		samples[index] = telemetry.Sample{Timestamp: time.Unix(int64(index), 0), Value: 10}
	}
	samples[53].Value = 95

	got := DownsampleMinMax(samples, 10)
	if len(got) > 20 {
		t.Fatalf("points = %d, want at most 20", len(got))
	}
	foundSpike := false
	for index, sample := range got {
		if sample.Value == 95 {
			foundSpike = true
		}
		if index > 0 && sample.Timestamp.Before(got[index-1].Timestamp) {
			t.Fatalf("point %d is out of chronological order", index)
		}
	}
	if !foundSpike {
		t.Fatal("spike was discarded")
	}
}

func TestDownsampleMinMaxCopiesSmallInput(t *testing.T) {
	samples := []telemetry.Sample{{Value: 1}, {Value: 2}}
	got := DownsampleMinMax(samples, 2)
	got[0].Value = 99
	if samples[0].Value != 1 {
		t.Fatal("downsample result aliases input")
	}
}

func TestDownsampleMinMaxReturnsEmptyForInvalidWidth(t *testing.T) {
	if got := DownsampleMinMax([]telemetry.Sample{{Value: 1}}, 0); len(got) != 0 {
		t.Fatalf("points = %d, want 0", len(got))
	}
}
