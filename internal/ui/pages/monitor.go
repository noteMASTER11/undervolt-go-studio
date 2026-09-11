package pages

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/noteMASTER11/undervolt-go-studio/internal/hardware"
	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
	"github.com/noteMASTER11/undervolt-go-studio/internal/ui/components"
	"github.com/noteMASTER11/undervolt-go-studio/internal/ui/viewmodel"
)

type Monitor struct {
	vm          *viewmodel.Monitor
	catalog     telemetry.Catalog
	descriptors map[telemetry.MetricID]telemetry.Descriptor
	selected    map[telemetry.MetricID]bool
	intervals   []time.Duration
	active      bool

	search     *widget.Entry
	interval   *widget.Select
	pause      *widget.Button
	metricList *fyne.Container
	values     *fyne.Container
	timeline   *components.Timeline
	root       fyne.CanvasObject
}

func NewMonitor(source viewmodel.SubscriptionSource, catalog telemetry.Catalog) *Monitor {
	page := &Monitor{
		vm:          viewmodel.NewMonitor(source, 250*time.Millisecond),
		descriptors: make(map[telemetry.MetricID]telemetry.Descriptor),
		selected:    make(map[telemetry.MetricID]bool),
		intervals:   []time.Duration{100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second, 5 * time.Second},
		metricList:  container.NewVBox(), values: container.NewVBox(), timeline: components.NewTimeline(),
	}
	page.search = widget.NewEntry()
	page.search.SetPlaceHolder("Search metrics")
	page.search.OnChanged = page.rebuildMetricList
	options := make([]string, len(page.intervals))
	for index, interval := range page.intervals {
		options[index] = intervalLabel(interval)
	}
	page.interval = widget.NewSelect(options, func(value string) {
		for index, label := range options {
			if label == value {
				page.vm.SetInterval(page.intervals[index])
				return
			}
		}
	})
	page.interval.SetSelected(intervalLabel(250 * time.Millisecond))
	page.pause = widget.NewButton("Pause", page.togglePause)
	controls := container.NewGridWithColumns(3, page.search, page.interval, page.pause)
	left := container.NewBorder(widget.NewLabelWithStyle("Metrics", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), nil, nil, nil, container.NewVScroll(page.metricList))
	center := container.NewBorder(controls, container.NewVScroll(page.values), nil, nil, page.timeline.Object())
	page.root = container.NewPadded(container.NewBorder(
		container.NewVBox(widget.NewLabelWithStyle("Live Monitor", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), widget.NewLabel("Only selected metrics are sampled")),
		nil, container.NewGridWrap(fyne.NewSize(280, 520), left), nil, center,
	))
	page.vm.SetListener(func(state viewmodel.MonitorState) {
		fyne.Do(func() { page.render(state) })
	})
	page.SetCatalog(catalog)
	return page
}

func (p *Monitor) ID() string                { return "monitor" }
func (p *Monitor) Object() fyne.CanvasObject { return p.root }
func (p *Monitor) Activate() {
	if p.active {
		return
	}
	p.active = true
	p.pause.SetText("Pause")
	p.vm.Activate()
}
func (p *Monitor) Deactivate() {
	if !p.active {
		return
	}
	p.active = false
	p.vm.Deactivate()
}

func (p *Monitor) SetCatalog(catalog telemetry.Catalog) {
	p.catalog = cloneCatalog(catalog)
	p.descriptors = descriptorMap(catalog)
	if len(p.selected) == 0 {
		for index, descriptor := range catalog.Metrics {
			if index == 4 {
				break
			}
			p.selected[descriptor.ID] = true
		}
	}
	p.applySelection()
	p.rebuildMetricList(p.search.Text)
}

func (p *Monitor) togglePause() {
	if p.active {
		p.vm.Deactivate()
		p.active = false
		p.pause.SetText("Resume")
		return
	}
	p.vm.Activate()
	p.active = true
	p.pause.SetText("Pause")
}

func (p *Monitor) rebuildMetricList(query string) {
	query = strings.ToLower(strings.TrimSpace(query))
	metrics := append([]telemetry.Descriptor(nil), p.catalog.Metrics...)
	sort.Slice(metrics, func(i, j int) bool {
		if metrics[i].DeviceID == metrics[j].DeviceID {
			return metrics[i].Label < metrics[j].Label
		}
		return metrics[i].DeviceID < metrics[j].DeviceID
	})
	objects := make([]fyne.CanvasObject, 0, len(metrics)+4)
	var deviceID string
	for _, descriptor := range metrics {
		if query != "" && !strings.Contains(strings.ToLower(descriptor.Label+" "+string(descriptor.ID)), query) {
			continue
		}
		if string(descriptor.DeviceID) != deviceID {
			deviceID = string(descriptor.DeviceID)
			objects = append(objects, widget.NewLabelWithStyle(deviceID, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
		}
		metricID := descriptor.ID
		check := widget.NewCheck(descriptor.Label, nil)
		check.SetChecked(p.selected[metricID])
		check.OnChanged = func(enabled bool) {
			p.selected[metricID] = enabled
			p.applySelection()
		}
		objects = append(objects, check)
	}
	p.metricList.Objects = objects
	p.metricList.Refresh()
}

func (p *Monitor) applySelection() {
	metricIDs := make([]telemetry.MetricID, 0, len(p.selected))
	for _, descriptor := range p.catalog.Metrics {
		if p.selected[descriptor.ID] {
			metricIDs = append(metricIDs, descriptor.ID)
		}
	}
	p.vm.SetMetricIDs(metricIDs)
	p.render(p.vm.State())
}

func (p *Monitor) render(state viewmodel.MonitorState) {
	now := time.Now()
	valueObjects := make([]fyne.CanvasObject, 0, len(state.Selected))
	series := make([]components.Series, 0, len(state.Selected))
	for index, metricID := range state.Selected {
		descriptor := p.descriptors[metricID]
		value := "— Loading"
		if sample, exists := state.Current[metricID]; exists {
			quality := sample.QualityAt(now, state.Interval*3)
			switch quality {
			case telemetry.QualityGood:
				value = fmt.Sprintf("%.1f %s", sample.Value, descriptor.Unit)
			case telemetry.QualityStale:
				value = fmt.Sprintf("%.1f %s · stale", sample.Value, descriptor.Unit)
			default:
				value = "— Unavailable"
				if sample.Error != "" {
					value += ": " + sample.Error
				}
			}
		}
		valueObjects = append(valueObjects, container.NewBorder(nil, nil, widget.NewLabel(descriptor.Label), nil, widget.NewLabel(value)))
		series = append(series, components.Series{
			ID: metricID, Label: descriptor.Label, Unit: descriptor.Unit,
			Color: chartColors[index%len(chartColors)], Points: state.History[metricID],
		})
	}
	p.values.Objects = valueObjects
	p.values.Refresh()
	p.timeline.SetSeries(series)
}

func intervalLabel(interval time.Duration) string {
	if interval < time.Second {
		return fmt.Sprintf("%d ms", interval.Milliseconds())
	}
	return fmt.Sprintf("%g s", interval.Seconds())
}

func descriptorMap(catalog telemetry.Catalog) map[telemetry.MetricID]telemetry.Descriptor {
	result := make(map[telemetry.MetricID]telemetry.Descriptor, len(catalog.Metrics))
	for _, descriptor := range catalog.Metrics {
		result[descriptor.ID] = descriptor
	}
	return result
}

func cloneCatalog(catalog telemetry.Catalog) telemetry.Catalog {
	return telemetry.Catalog{
		Devices: append([]hardware.Device(nil), catalog.Devices...),
		Metrics: append([]telemetry.Descriptor(nil), catalog.Metrics...),
	}
}
