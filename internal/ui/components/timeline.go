package components

import (
	"fmt"
	"image/color"
	"math"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
)

type Series struct {
	ID     telemetry.MetricID
	Label  string
	Unit   telemetry.Unit
	Color  color.Color
	Points []telemetry.Sample
}

type Timeline struct {
	mu     sync.RWMutex
	series []Series
	render []renderSeries
	raster *canvas.Raster
	root   fyne.CanvasObject

	minimum      float64
	maximum      float64
	minimumLabel *widget.Label
	maximumLabel *widget.Label
	startLabel   *widget.Label
	endLabel     *widget.Label
	legendLabel  *widget.Label
}

type renderSeries struct {
	series  Series
	minimum float64
	maximum float64
}

func NewTimeline() *Timeline {
	timeline := &Timeline{
		minimumLabel: widget.NewLabel("—"),
		maximumLabel: widget.NewLabel("—"),
		startLabel:   widget.NewLabel(""),
		endLabel:     widget.NewLabel(""),
		legendLabel:  widget.NewLabel("Waiting for samples"),
	}
	timeline.raster = canvas.NewRasterWithPixels(timeline.pixel)
	timeline.raster.SetMinSize(fyne.NewSize(360, 170))
	yAxis := container.NewVBox(timeline.maximumLabel, layout.NewSpacer(), timeline.minimumLabel)
	xAxis := container.NewBorder(nil, nil, timeline.startLabel, timeline.endLabel)
	timeline.root = container.NewBorder(timeline.legendLabel, xAxis, yAxis, nil, timeline.raster)
	return timeline
}

func (t *Timeline) Object() fyne.CanvasObject { return t.root }

func (t *Timeline) SetSeries(series []Series) {
	cloned := cloneSeries(series)
	render := make([]renderSeries, 0, len(cloned))
	minimum, maximum := 0.0, 0.0
	hasValues := false
	var earliest, latest time.Time
	legend := make([]string, 0, len(cloned))
	for _, item := range cloned {
		if len(item.Points) == 0 {
			continue
		}
		seriesMinimum, seriesMaximum := item.Points[0].Value, item.Points[0].Value
		for _, point := range item.Points[1:] {
			seriesMinimum = math.Min(seriesMinimum, point.Value)
			seriesMaximum = math.Max(seriesMaximum, point.Value)
		}
		if !hasValues {
			minimum, maximum, hasValues = seriesMinimum, seriesMaximum, true
		} else {
			minimum = math.Min(minimum, seriesMinimum)
			maximum = math.Max(maximum, seriesMaximum)
		}
		first, last := item.Points[0].Timestamp, item.Points[len(item.Points)-1].Timestamp
		if !first.IsZero() && (earliest.IsZero() || first.Before(earliest)) {
			earliest = first
		}
		if !last.IsZero() && last.After(latest) {
			latest = last
		}
		legend = append(legend, item.Label)
		render = append(render, renderSeries{series: item})
	}
	if hasValues && maximum == minimum {
		maximum = minimum + 1
	}
	for index := range render {
		render[index].minimum = minimum
		render[index].maximum = maximum
	}
	t.mu.Lock()
	t.series = cloned
	t.render = render
	t.minimum = minimum
	t.maximum = maximum
	unit := telemetry.Unit("")
	if len(render) > 0 {
		unit = render[0].series.Unit
	}
	if hasValues {
		t.minimumLabel.SetText(formatAxisValue(minimum, unit))
		t.maximumLabel.SetText(formatAxisValue(maximum, unit))
	} else {
		t.minimumLabel.SetText("—")
		t.maximumLabel.SetText("—")
	}
	if earliest.IsZero() {
		t.startLabel.SetText("")
		t.endLabel.SetText("")
	} else {
		t.startLabel.SetText(earliest.Format("15:04:05"))
		t.endLabel.SetText(latest.Format("15:04:05"))
	}
	if len(legend) == 0 {
		t.legendLabel.SetText("Waiting for samples")
	} else {
		t.legendLabel.SetText(strings.Join(legend, "  ·  "))
	}
	t.mu.Unlock()
	t.raster.Refresh()
}

func (t *Timeline) Series() []Series {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return cloneSeries(t.series)
}

func (t *Timeline) pixel(x, y, width, height int) color.Color {
	t.mu.RLock()
	defer t.mu.RUnlock()
	background := color.NRGBA{R: 20, G: 25, B: 32, A: 255}
	if width < 2 || height < 2 {
		return background
	}
	xStep := width / 4
	yStep := height / 4
	if (xStep > 0 && x%xStep == 0) || (yStep > 0 && y%yStep == 0) {
		background = color.NRGBA{R: 38, G: 45, B: 55, A: 255}
	}
	for _, render := range t.render {
		series := render.series
		if len(series.Points) < 2 {
			continue
		}
		position := float64(x) / float64(width-1) * float64(len(series.Points)-1)
		segment := int(math.Floor(position))
		if segment >= len(series.Points)-1 {
			segment = len(series.Points) - 2
		}
		distance := pixelSegmentDistance(x, y, width, height, segment, len(series.Points), series.Points, render.minimum, render.maximum)
		if segment > 0 {
			distance = math.Min(distance, pixelSegmentDistance(x, y, width, height, segment-1, len(series.Points), series.Points, render.minimum, render.maximum))
		}
		const solidRadius = 1.5
		const antialiasRadius = 2.5
		if distance <= antialiasRadius {
			lineColor := series.Color
			if lineColor == nil {
				lineColor = color.NRGBA{R: 56, G: 189, B: 248, A: 255}
			}
			opacity := 1.0
			if distance > solidRadius {
				opacity = antialiasRadius - distance
			}
			return blendColor(background, lineColor, opacity)
		}
	}
	return background
}

func pixelSegmentDistance(x, y, width, height, segment, count int, points []telemetry.Sample, minimum, maximum float64) float64 {
	x1 := float64(segment) / float64(count-1) * float64(width-1)
	x2 := float64(segment+1) / float64(count-1) * float64(width-1)
	y1 := float64(height-1) - (points[segment].Value-minimum)/(maximum-minimum)*float64(height-1)
	y2 := float64(height-1) - (points[segment+1].Value-minimum)/(maximum-minimum)*float64(height-1)
	return pointSegmentDistance(float64(x), float64(y), x1, y1, x2, y2)
}

func pointSegmentDistance(px, py, x1, y1, x2, y2 float64) float64 {
	dx, dy := x2-x1, y2-y1
	lengthSquared := dx*dx + dy*dy
	if lengthSquared == 0 {
		return math.Hypot(px-x1, py-y1)
	}
	projection := ((px-x1)*dx + (py-y1)*dy) / lengthSquared
	projection = math.Max(0, math.Min(1, projection))
	return math.Hypot(px-(x1+projection*dx), py-(y1+projection*dy))
}

func blendColor(background, foreground color.Color, opacity float64) color.Color {
	br, bg, bb, _ := background.RGBA()
	fr, fg, fb, fa := foreground.RGBA()
	opacity *= float64(fa) / 65535
	return color.NRGBA{
		R: uint8((float64(br)*(1-opacity) + float64(fr)*opacity) / 257),
		G: uint8((float64(bg)*(1-opacity) + float64(fg)*opacity) / 257),
		B: uint8((float64(bb)*(1-opacity) + float64(fb)*opacity) / 257),
		A: 255,
	}
}

func formatAxisValue(value float64, unit telemetry.Unit) string {
	if unit == "" {
		return fmt.Sprintf("%.1f", value)
	}
	return fmt.Sprintf("%.1f %s", value, unit)
}

func cloneSeries(series []Series) []Series {
	result := make([]Series, len(series))
	for index := range series {
		result[index] = series[index]
		result[index].Points = append([]telemetry.Sample(nil), series[index].Points...)
	}
	return result
}
