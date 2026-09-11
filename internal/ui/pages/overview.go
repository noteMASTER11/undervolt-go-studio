package pages

import (
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
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

type overviewMetrics struct {
	utilization telemetry.MetricID
	temperature telemetry.MetricID
	frequencies []telemetry.MetricID
}

type Overview struct {
	vm               *viewmodel.Overview
	catalog          telemetry.Catalog
	descriptors      map[telemetry.MetricID]telemetry.Descriptor
	metrics          overviewMetrics
	cards            map[string]*components.MetricCard
	cardGrid         *fyne.Container
	charts           map[string]*components.Timeline
	chartHost        *fyne.Container
	updates          *components.LatestDispatcher[viewmodel.MonitorState]
	frequencyHistory []telemetry.Sample
	afterRender      func(viewmodel.MonitorState)
	root             fyne.CanvasObject
}

func NewOverview(source viewmodel.SubscriptionSource, catalog telemetry.Catalog) *Overview {
	return newOverview(source, catalog, nil)
}

// NewOverviewWithRenderCallback reports each completed UI render to deterministic snapshot callers.
func NewOverviewWithRenderCallback(source viewmodel.SubscriptionSource, catalog telemetry.Catalog, afterRender func(viewmodel.MonitorState)) *Overview {
	return newOverview(source, catalog, afterRender)
}

func newOverview(source viewmodel.SubscriptionSource, catalog telemetry.Catalog, afterRender func(viewmodel.MonitorState)) *Overview {
	page := &Overview{
		vm:          viewmodel.NewOverview(source, catalog, 250*time.Millisecond),
		descriptors: make(map[telemetry.MetricID]telemetry.Descriptor),
		cards:       make(map[string]*components.MetricCard),
		cardGrid:    container.NewGridWithColumns(3),
		charts:      make(map[string]*components.Timeline),
		chartHost:   container.NewStack(),
		afterRender: afterRender,
	}
	page.root = container.NewPadded(container.NewBorder(
		container.NewVBox(
			widget.NewLabelWithStyle("System Overview", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			widget.NewLabel("Live CPU telemetry · independent scales · 60-second window"),
			page.cardGrid,
		), nil, nil, nil, page.chartHost,
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
	p.metrics = classifyOverviewMetrics(p.vm.State().Selected, p.descriptors)
	p.frequencyHistory = nil
	p.rebuild(p.vm.State())
}

func (p *Overview) rebuild(state viewmodel.MonitorState) {
	p.cards = make(map[string]*components.MetricCard, 3)
	cardObjects := make([]fyne.CanvasObject, 0, 3)
	addCard := func(key, label string, unit telemetry.Unit) {
		card := components.NewMetricCard(label, unit)
		p.cards[key] = card
		cardObjects = append(cardObjects, card.Object())
	}
	if p.metrics.utilization != "" {
		addCard("utilization", "CPU Load", "%")
	}
	if p.metrics.temperature != "" {
		addCard("temperature", "Package Temperature", "°C")
	}
	if len(p.metrics.frequencies) > 0 {
		addCard("frequency", "Average CPU Frequency", "MHz")
	}
	p.cardGrid.Objects = cardObjects
	p.cardGrid.Refresh()

	p.charts = make(map[string]*components.Timeline, 3)
	chartObjects := make([]fyne.CanvasObject, 0, 3)
	addChart := func(key, title string) {
		timeline := components.NewTimeline()
		p.charts[key] = timeline
		chartObjects = append(chartObjects, widget.NewCard(title, "", timeline.Object()))
	}
	if p.metrics.utilization != "" {
		addChart("utilization", "CPU Load")
	}
	if p.metrics.temperature != "" {
		addChart("temperature", "Package Temperature")
	}
	if len(p.metrics.frequencies) > 0 {
		addChart("frequency", "Average CPU Frequency")
	}
	p.chartHost.Objects = []fyne.CanvasObject{overviewChartLayout(chartObjects)}
	p.chartHost.Refresh()
	p.render(state)
}

func (p *Overview) render(state viewmodel.MonitorState) {
	now := time.Now()
	cutoff := now.Add(-60 * time.Second)
	if metricID := p.metrics.utilization; metricID != "" {
		p.renderDirect("utilization", metricID, state, now, cutoff, chartColors[0])
	}
	if metricID := p.metrics.temperature; metricID != "" {
		p.renderDirect("temperature", metricID, state, now, cutoff, chartColors[1])
	}
	if len(p.metrics.frequencies) > 0 {
		if sample, ok := averageSample(state.Current, p.metrics.frequencies); ok {
			p.cards["frequency"].SetSample(sample, sample.QualityAt(now, state.Interval*3))
		}
		p.appendAverageFrequency(state)
		p.charts["frequency"].SetSeries([]components.Series{{
			ID: "cpu.average.frequency", Label: "Average", Unit: "MHz", Color: chartColors[2],
			Points: recentSamples(p.frequencyHistory, cutoff),
		}})
	}
	if p.afterRender != nil {
		p.afterRender(state)
	}
}

func (p *Overview) appendAverageFrequency(state viewmodel.MonitorState) {
	sample, ok := averageSample(state.Current, p.metrics.frequencies)
	if !ok || sample.Timestamp.IsZero() {
		return
	}
	if count := len(p.frequencyHistory); count > 0 && !sample.Timestamp.After(p.frequencyHistory[count-1].Timestamp) {
		return
	}
	p.frequencyHistory = append(p.frequencyHistory, sample)
	const capacity = 600
	if len(p.frequencyHistory) > capacity {
		p.frequencyHistory = append([]telemetry.Sample(nil), p.frequencyHistory[len(p.frequencyHistory)-capacity:]...)
	}
}

func (p *Overview) renderDirect(key string, metricID telemetry.MetricID, state viewmodel.MonitorState, now, cutoff time.Time, lineColor color.Color) {
	descriptor := p.descriptors[metricID]
	if sample, exists := state.Current[metricID]; exists {
		p.cards[key].SetSample(sample, sample.QualityAt(now, state.Interval*3))
	}
	p.charts[key].SetSeries([]components.Series{{
		ID: metricID, Label: descriptor.Label, Unit: descriptor.Unit, Color: lineColor,
		Points: recentSamples(state.History[metricID], cutoff),
	}})
}

func classifyOverviewMetrics(selected []telemetry.MetricID, descriptors map[telemetry.MetricID]telemetry.Descriptor) overviewMetrics {
	var result overviewMetrics
	for _, metricID := range selected {
		descriptor := descriptors[metricID]
		switch descriptor.Unit {
		case "%":
			if result.utilization == "" {
				result.utilization = metricID
			}
		case "°C":
			if result.temperature == "" {
				result.temperature = metricID
			}
		case "MHz":
			result.frequencies = append(result.frequencies, metricID)
		}
	}
	return result
}

func overviewChartLayout(charts []fyne.CanvasObject) fyne.CanvasObject {
	switch len(charts) {
	case 0:
		return container.NewCenter(widget.NewLabel("Waiting for hardware discovery…"))
	case 1:
		return charts[0]
	case 2:
		return container.NewGridWithColumns(2, overviewChartCell(charts[0]), overviewChartCell(charts[1]))
	default:
		return container.NewGridWithRows(3, overviewChartCell(charts[0]), overviewChartCell(charts[1]), overviewChartCell(charts[2]))
	}
}

func overviewChartCell(chart fyne.CanvasObject) fyne.CanvasObject {
	rightInset := canvas.NewRectangle(color.Transparent)
	rightInset.SetMinSize(fyne.NewSize(48, 1))
	return container.NewPadded(container.NewBorder(nil, nil, nil, rightInset, chart))
}

func averageSample(samples map[telemetry.MetricID]telemetry.Sample, metricIDs []telemetry.MetricID) (telemetry.Sample, bool) {
	var total float64
	var count int
	var timestamp time.Time
	quality := telemetry.QualityGood
	for _, metricID := range metricIDs {
		sample, exists := samples[metricID]
		if !exists || sample.Quality == telemetry.QualityUnavailable {
			continue
		}
		total += sample.Value
		count++
		if sample.Timestamp.After(timestamp) {
			timestamp = sample.Timestamp
		}
		if sample.Quality == telemetry.QualityStale {
			quality = telemetry.QualityStale
		}
	}
	if count == 0 {
		return telemetry.Sample{}, false
	}
	return telemetry.Sample{MetricID: "cpu.average.frequency", Value: total / float64(count), Timestamp: timestamp, Quality: quality}, true
}

func recentSamples(samples []telemetry.Sample, cutoff time.Time) []telemetry.Sample {
	first := 0
	for first < len(samples) && samples[first].Timestamp.Before(cutoff) {
		first++
	}
	return append([]telemetry.Sample(nil), samples[first:]...)
}
