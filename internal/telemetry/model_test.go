package telemetry

import (
	"testing"
	"time"
)

func TestSampleQualityAtMarksExpiredGoodValueStale(t *testing.T) {
	now := time.Unix(100, 0)
	sample := Sample{Timestamp: now, Quality: QualityGood}

	if got := sample.QualityAt(now.Add(250*time.Millisecond), 250*time.Millisecond); got != QualityGood {
		t.Fatalf("quality at freshness boundary = %q, want %q", got, QualityGood)
	}
	if got := sample.QualityAt(now.Add(250*time.Millisecond+time.Nanosecond), 250*time.Millisecond); got != QualityStale {
		t.Fatalf("quality after freshness boundary = %q, want %q", got, QualityStale)
	}
}

func TestSampleQualityAtPreservesUnavailable(t *testing.T) {
	now := time.Unix(100, 0)
	sample := Sample{Timestamp: now, Quality: QualityUnavailable}
	if got := sample.QualityAt(now.Add(time.Hour), time.Second); got != QualityUnavailable {
		t.Fatalf("quality = %q, want %q", got, QualityUnavailable)
	}
}
