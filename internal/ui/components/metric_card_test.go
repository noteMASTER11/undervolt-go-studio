package components

import (
	"testing"
	"time"

	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
)

func TestMetricCardDistinguishesUnavailableAndStale(t *testing.T) {
	card := NewMetricCard("CPU temperature", "°C")
	card.SetSample(telemetry.Sample{Quality: telemetry.QualityUnavailable, Error: "missing"})
	if got := card.value.Text; got != "—" {
		t.Fatalf("unavailable value = %q", got)
	}
	if got := card.status.Text; got != "Unavailable" {
		t.Fatalf("unavailable status = %q", got)
	}

	card.SetSample(telemetry.Sample{Value: 73.5, Quality: telemetry.QualityGood, Timestamp: time.Now().Add(-time.Second)}, telemetry.QualityStale)
	if got := card.value.Text; got != "73.5 °C" {
		t.Fatalf("stale value = %q", got)
	}
	if got := card.status.Text; got != "Stale" {
		t.Fatalf("stale status = %q", got)
	}
}
