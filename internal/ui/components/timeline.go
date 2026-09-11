package components

import (
	"image/color"
	"math"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"

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
}

type renderSeries struct {
	series  Series
	minimum float64
	maximum float64
}

func NewTimeline() *Timeline {
	timeline := &Timeline{}
	timeline.raster = canvas.NewRasterWithPixels(timeline.pixel)
	timeline.raster.SetMinSize(fyne.NewSize(480, 220))
	return timeline
}

func (t *Timeline) Object() fyne.CanvasObject { return t.raster }

func (t *Timeline) SetSeries(series []Series) {
	cloned := cloneSeries(series)
	render := make([]renderSeries, 0, len(cloned))
	for _, item := range cloned {
		if len(item.Points) == 0 {
			continue
		}
		minimum, maximum := item.Points[0].Value, item.Points[0].Value
		for _, point := range item.Points[1:] {
			minimum = math.Min(minimum, point.Value)
			maximum = math.Max(maximum, point.Value)
		}
		if maximum == minimum {
			maximum = minimum + 1
		}
		render = append(render, renderSeries{series: item, minimum: minimum, maximum: maximum})
	}
	t.mu.Lock()
	t.series = cloned
	t.render = render
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
	if x%80 == 0 || y%55 == 0 {
		background = color.NRGBA{R: 38, G: 45, B: 55, A: 255}
	}
	for _, render := range t.render {
		series := render.series
		if len(series.Points) < 2 {
			continue
		}
		pointIndex := int(float64(x) / float64(width-1) * float64(len(series.Points)-1))
		expectedY := height - 1 - int((series.Points[pointIndex].Value-render.minimum)/(render.maximum-render.minimum)*float64(height-1))
		if abs(y-expectedY) <= 1 {
			return series.Color
		}
	}
	return background
}

func cloneSeries(series []Series) []Series {
	result := make([]Series, len(series))
	for index := range series {
		result[index] = series[index]
		result[index].Points = append([]telemetry.Sample(nil), series[index].Points...)
	}
	return result
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
