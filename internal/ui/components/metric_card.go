package components

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
)

type MetricCard struct {
	unit   telemetry.Unit
	value  *widget.Label
	status *widget.Label
	card   *widget.Card
}

func NewMetricCard(label string, unit telemetry.Unit) *MetricCard {
	value := widget.NewLabelWithStyle("—", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	status := widget.NewLabel("Loading")
	return &MetricCard{
		unit: unit, value: value, status: status,
		card: widget.NewCard(label, "", container.NewVBox(value, status)),
	}
}

func (c *MetricCard) Object() fyne.CanvasObject { return c.card }

func (c *MetricCard) SetSample(sample telemetry.Sample, qualityOverride ...telemetry.Quality) {
	quality := sample.Quality
	if len(qualityOverride) > 0 {
		quality = qualityOverride[0]
	}
	switch quality {
	case telemetry.QualityGood:
		c.value.SetText(formatValue(sample.Value, c.unit))
		c.status.SetText("Live")
	case telemetry.QualityStale:
		c.value.SetText(formatValue(sample.Value, c.unit))
		c.status.SetText("Stale")
	default:
		c.value.SetText("—")
		c.status.SetText("Unavailable")
	}
}

func formatValue(value float64, unit telemetry.Unit) string {
	return fmt.Sprintf("%.1f %s", value, unit)
}
