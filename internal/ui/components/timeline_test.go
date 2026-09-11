package components

import (
	"image/color"
	"testing"
	"time"

	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
)

func TestTimelineCopiesSeriesInput(t *testing.T) {
	timeline := NewTimeline()
	series := []Series{{
		ID: "cpu.utilization", Label: "CPU", Unit: "%", Color: color.NRGBA{R: 1},
		Points: []telemetry.Sample{{MetricID: "cpu.utilization", Value: 25, Timestamp: time.Now()}},
	}}
	timeline.SetSeries(series)
	series[0].Label = "changed"
	series[0].Points[0].Value = 99

	got := timeline.Series()
	if got[0].Label != "CPU" || got[0].Points[0].Value != 25 {
		t.Fatalf("timeline retained caller storage: %+v", got[0])
	}
	got[0].Points[0].Value = 77
	if timeline.Series()[0].Points[0].Value != 25 {
		t.Fatal("Series returned internal storage")
	}
}
