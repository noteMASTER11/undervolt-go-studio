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
	raster *canvas.Raster
}

func NewTimeline() *Timeline {
	timeline := &Timeline{}
	timeline.raster = canvas.NewRasterWithPixels(timeline.pixel)
	timeline.raster.SetMinSize(fyne.NewSize(480, 220))
	return timeline
}

func (t *Timeline) Object() fyne.CanvasObject { return t.raster }

func (t *Timeline) SetSeries(series []Series) {
	t.mu.Lock()
	t.series = cloneSeries(series)
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
	for _, series := range t.series {
		if len(series.Points) < 2 {
			continue
		}
		minimum, maximum := series.Points[0].Value, series.Points[0].Value
		for _, point := range series.Points[1:] {
			minimum = math.Min(minimum, point.Value)
			maximum = math.Max(maximum, point.Value)
		}
		if maximum == minimum {
			maximum = minimum + 1
		}
		pointIndex := int(float64(x) / float64(width-1) * float64(len(series.Points)-1))
		expectedY := height - 1 - int((series.Points[pointIndex].Value-minimum)/(maximum-minimum)*float64(height-1))
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
