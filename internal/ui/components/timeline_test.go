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

func TestTimelinePrecomputesBoundsBeforePaint(t *testing.T) {
	timeline := NewTimeline()
	timeline.SetSeries([]Series{{Points: []telemetry.Sample{{Value: 8}, {Value: 2}, {Value: 11}}}})
	timeline.mu.RLock()
	defer timeline.mu.RUnlock()
	if got := len(timeline.render); got != 1 {
		t.Fatalf("render series = %d", got)
	}
	if timeline.render[0].minimum != 2 || timeline.render[0].maximum != 11 {
		t.Fatalf("bounds = %v..%v", timeline.render[0].minimum, timeline.render[0].maximum)
	}
}

func TestTimelineInterpolatesContinuousLine(t *testing.T) {
	lineColor := color.NRGBA{R: 255, A: 255}
	timeline := NewTimeline()
	timeline.SetSeries([]Series{{
		Unit: "%", Color: lineColor,
		Points: []telemetry.Sample{{Value: 0}, {Value: 10}},
	}})
	if got := timeline.pixel(5, 5, 11, 11); got != lineColor {
		t.Fatalf("middle pixel = %v, want line color %v", got, lineColor)
	}
}

func TestTimelineKeepsSteepSegmentsConnected(t *testing.T) {
	lineColor := color.NRGBA{R: 255, A: 255}
	timeline := NewTimeline()
	timeline.SetSeries([]Series{{
		Unit: "%", Color: lineColor,
		Points: []telemetry.Sample{{Value: 0}, {Value: 10}},
	}})
	if got := timeline.pixel(0, 7, 3, 11); got != lineColor {
		t.Fatalf("pixel beside steep segment = %v, want line color %v", got, lineColor)
	}
}

func TestTimelineAntialiasesThickLineEdge(t *testing.T) {
	lineColor := color.NRGBA{R: 255, A: 255}
	timeline := NewTimeline()
	timeline.SetSeries([]Series{{
		Unit: "%", Color: lineColor,
		Points: []telemetry.Sample{{Value: 5}, {Value: 5}},
	}})
	got := timeline.pixel(3, 10, 11, 13)
	red, green, blue, _ := got.RGBA()
	if red <= 20*257 || red >= 255*257 || green >= 25*257 || blue >= 32*257 {
		t.Fatalf("antialiased edge pixel = %v, want blend between background and line", got)
	}
}

func TestTimelineUsesOneScaleAndLabelsAxes(t *testing.T) {
	timeline := NewTimeline()
	timeline.SetSeries([]Series{
		{Label: "wide", Unit: "°C", Points: []telemetry.Sample{{Value: 0}, {Value: 100}}},
		{Label: "narrow", Unit: "°C", Points: []telemetry.Sample{{Value: 40}, {Value: 60}}},
	})
	timeline.mu.RLock()
	defer timeline.mu.RUnlock()
	if timeline.minimum != 0 || timeline.maximum != 100 {
		t.Fatalf("global bounds = %v..%v", timeline.minimum, timeline.maximum)
	}
	if timeline.minimumLabel.Text != "0.0 °C" || timeline.maximumLabel.Text != "100.0 °C" {
		t.Fatalf("axis labels = %q..%q", timeline.minimumLabel.Text, timeline.maximumLabel.Text)
	}
}
