package pages

import (
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
	"github.com/noteMASTER11/undervolt-go-studio/internal/ui/components"
	"github.com/noteMASTER11/undervolt-go-studio/internal/ui/viewmodel"
)

var chartColors = []color.Color{
	color.NRGBA{R: 56, G: 189, B: 248, A: 255},
	color.NRGBA{R: 251, G: 113, B: 133, A: 255},
	color.NRGBA{R: 74, G: 222, B: 128, A: 255},
	color.NRGBA{R: 250, G: 204, B: 21, A: 255},
}

type Overview struct {
	vm          *viewmodel.Overview
	catalog     telemetry.Catalog
	descriptors map[telemetry.MetricID]telemetry.Descriptor
	cards       map[telemetry.MetricID]*components.MetricCard
	cardGrid    *fyne.Container
	timeline    *components.Timeline
	updates     *components.LatestDispatcher[viewmodel.MonitorState]
	root        fyne.CanvasObject
}

func NewOverview(source viewmodel.SubscriptionSource, catalog telemetry.Catalog) *Overview {
	page := &Overview{
		vm:          viewmodel.NewOverview(source, catalog, 250*time.Millisecond),
		descriptors: make(map[telemetry.MetricID]telemetry.Descriptor),
		cards:       make(map[telemetry.MetricID]*components.MetricCard),
		cardGrid:    container.NewGridWithColumns(4),
		timeline:    components.NewTimeline(),
	}
	page.root = container.NewPadded(container.NewBorder(
		container.NewVBox(
			widget.NewLabelWithStyle("System Overview", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			widget.NewLabel("Live summary · 60-second rolling history"),
			page.cardGrid,
		), nil, nil, nil,
		container.NewPadded(page.timeline.Object()),
	))
	page.updates = components.NewLatestDispatcher(fyne.Do, page.render)
	page.vm.SetListener(page.updates.Submit)
	page.SetCatalog(catalog)
	return page
}

func (p *Overview) ID() string                { return "overview" }
func (p *Overview) Object() fyne.CanvasObject { return p.root }
func (p *Overview) Activate()                 { p.vm.Activate() }
func (p *Overview) Deactivate()               { p.vm.Deactivate() }

func (p *Overview) SetCatalog(catalog telemetry.Catalog) {
	p.catalog = cloneCatalog(catalog)
	p.descriptors = descriptorMap(catalog)
	p.vm.SetCatalog(catalog)
	p.rebuildCards(p.vm.State())
}

func (p *Overview) rebuildCards(state viewmodel.MonitorState) {
	p.cards = make(map[telemetry.MetricID]*components.MetricCard, len(state.Selected))
	objects := make([]fyne.CanvasObject, 0, 4)
	for _, metricID := range state.Selected {
		descriptor := p.descriptors[metricID]
		card := components.NewMetricCard(descriptor.Label, descriptor.Unit)
		p.cards[metricID] = card
		objects = append(objects, card.Object())
	}
	for len(objects) < 4 {
		labels := []string{"CPU utilization", "CPU temperature", "Effective frequency", "Fan speed"}
		objects = append(objects, components.NewMetricCard(labels[len(objects)], "").Object())
	}
	p.cardGrid.Objects = objects
	p.cardGrid.Refresh()
	p.render(state)
}

func (p *Overview) render(state viewmodel.MonitorState) {
	now := time.Now()
	series := make([]components.Series, 0, len(state.Selected))
	for index, metricID := range state.Selected {
		descriptor := p.descriptors[metricID]
		if sample, exists := state.Current[metricID]; exists {
			p.cards[metricID].SetSample(sample, sample.QualityAt(now, state.Interval*3))
		}
		series = append(series, components.Series{
			ID: metricID, Label: descriptor.Label, Unit: descriptor.Unit,
			Color: chartColors[index%len(chartColors)], Points: recentSamples(state.History[metricID], now.Add(-60*time.Second)),
		})
	}
	p.timeline.SetSeries(series)
}

func recentSamples(samples []telemetry.Sample, cutoff time.Time) []telemetry.Sample {
	first := 0
	for first < len(samples) && samples[first].Timestamp.Before(cutoff) {
		first++
	}
	return append([]telemetry.Sample(nil), samples[first:]...)
}
