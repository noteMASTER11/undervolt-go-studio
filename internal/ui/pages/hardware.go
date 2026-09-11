package pages

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/noteMASTER11/undervolt-go-studio/internal/product"
	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
)

type HardwareSource interface {
	Diagnostics() []telemetry.ProviderDiagnostics
}

type diagnosticsRow struct {
	Provider  string
	State     string
	Success   string
	Latency   string
	Metrics   int
	Consumers int
	Dropped   uint64
	Error     string
}

type Hardware struct {
	info    product.Info
	source  HardwareSource
	catalog telemetry.Catalog

	mu      sync.Mutex
	active  bool
	stop    chan struct{}
	done    chan struct{}
	rows    []diagnosticsRow
	devices []string

	providerList *widget.List
	deviceList   *widget.List
	status       *widget.Label
	root         fyne.CanvasObject
}

func NewHardware(info product.Info, source HardwareSource, catalog telemetry.Catalog) *Hardware {
	page := &Hardware{info: info, source: source, catalog: cloneCatalog(catalog)}
	page.providerList = widget.NewList(
		func() int { page.mu.Lock(); defer page.mu.Unlock(); return len(page.rows) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(id widget.ListItemID, object fyne.CanvasObject) {
			page.mu.Lock()
			defer page.mu.Unlock()
			if id >= len(page.rows) {
				return
			}
			row := page.rows[id]
			object.(*widget.Label).SetText(fmt.Sprintf(
				"%s  |  %s  |  latency %s  |  metrics %d  |  consumers %d  |  dropped %d  |  %s",
				row.Provider, row.State, row.Latency, row.Metrics, row.Consumers, row.Dropped, row.Error,
			))
		},
	)
	page.deviceList = widget.NewList(
		func() int { page.mu.Lock(); defer page.mu.Unlock(); return len(page.devices) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(id widget.ListItemID, object fyne.CanvasObject) {
			page.mu.Lock()
			defer page.mu.Unlock()
			if id < len(page.devices) {
				object.(*widget.Label).SetText(page.devices[id])
			}
		},
	)
	page.status = widget.NewLabel("Diagnostics refresh only while this page is active")
	copyButton := widget.NewButton("Copy Diagnostics", page.copyDiagnostics)
	header := container.NewBorder(nil, nil, nil, copyButton, container.NewVBox(
		widget.NewLabelWithStyle("Hardware & Providers", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		page.status,
	))
	body := container.NewGridWithRows(2,
		container.NewBorder(widget.NewLabelWithStyle("Discovered devices and metrics", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), nil, nil, nil, page.deviceList),
		container.NewBorder(widget.NewLabelWithStyle("Provider health", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), nil, nil, nil, page.providerList),
	)
	page.root = container.NewPadded(container.NewBorder(header, nil, nil, nil, body))
	page.SetCatalog(catalog)
	page.setDiagnostics(source.Diagnostics())
	return page
}

func (p *Hardware) ID() string                { return "hardware" }
func (p *Hardware) Object() fyne.CanvasObject { return p.root }

func (p *Hardware) Activate() {
	p.mu.Lock()
	if p.active {
		p.mu.Unlock()
		return
	}
	p.active = true
	p.stop = make(chan struct{})
	p.done = make(chan struct{})
	stop, done := p.stop, p.done
	p.mu.Unlock()
	go p.refreshLoop(stop, done)
}

func (p *Hardware) Deactivate() {
	p.mu.Lock()
	if !p.active {
		p.mu.Unlock()
		return
	}
	p.active = false
	stop, done := p.stop, p.done
	p.stop, p.done = nil, nil
	p.mu.Unlock()
	close(stop)
	<-done
}

func (p *Hardware) SetCatalog(catalog telemetry.Catalog) {
	devices := make([]string, 0, len(catalog.Devices)+len(catalog.Metrics))
	for _, device := range catalog.Devices {
		devices = append(devices, fmt.Sprintf("%s · %s · %s", device.Kind, device.Vendor, device.Name))
		for _, descriptor := range catalog.Metrics {
			if descriptor.DeviceID == device.ID {
				devices = append(devices, fmt.Sprintf("    %s [%s] · %s", descriptor.Label, descriptor.Unit, descriptor.ProviderID))
			}
		}
	}
	p.mu.Lock()
	p.catalog = cloneCatalog(catalog)
	p.devices = devices
	p.mu.Unlock()
	if p.deviceList != nil {
		p.deviceList.Refresh()
	}
}

func (p *Hardware) refreshLoop(stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			rows := diagnosticsRows(p.source.Diagnostics())
			fyne.Do(func() { p.applyRows(rows) })
		}
	}
}

func (p *Hardware) setDiagnostics(diagnostics []telemetry.ProviderDiagnostics) {
	p.applyRows(diagnosticsRows(diagnostics))
}

func (p *Hardware) applyRows(rows []diagnosticsRow) {
	p.mu.Lock()
	p.rows = append([]diagnosticsRow(nil), rows...)
	p.mu.Unlock()
	p.providerList.Refresh()
	p.status.SetText(fmt.Sprintf("%d providers · refreshes every second while active", len(rows)))
}

func (p *Hardware) copyDiagnostics() {
	p.mu.Lock()
	catalog := cloneCatalog(p.catalog)
	p.mu.Unlock()
	payload, err := diagnosticsJSON(p.info, catalog, p.source.Diagnostics())
	if err != nil {
		p.status.SetText("Could not encode diagnostics: " + err.Error())
		return
	}
	application := fyne.CurrentApp()
	if application == nil || application.Driver() == nil || len(application.Driver().AllWindows()) == 0 {
		p.status.SetText("Clipboard is unavailable")
		return
	}
	application.Driver().AllWindows()[0].Clipboard().SetContent(string(payload))
	p.status.SetText("Diagnostics copied")
}

func diagnosticsRows(diagnostics []telemetry.ProviderDiagnostics) []diagnosticsRow {
	rows := make([]diagnosticsRow, len(diagnostics))
	for index, item := range diagnostics {
		success := "never"
		if !item.LastSuccess.IsZero() {
			success = item.LastSuccess.Format(time.RFC3339)
		}
		rows[index] = diagnosticsRow{
			Provider: item.ProviderID, State: item.State, Success: success,
			Latency: item.LastLatency.Round(time.Microsecond).String(), Metrics: item.ActiveMetrics,
			Consumers: item.ActiveConsumers, Dropped: item.DroppedFrames, Error: item.LastError,
		}
	}
	return rows
}

func diagnosticsJSON(info product.Info, catalog telemetry.Catalog, diagnostics []telemetry.ProviderDiagnostics) ([]byte, error) {
	type safeDevice struct {
		ID     string `json:"id"`
		Kind   string `json:"kind"`
		Vendor string `json:"vendor"`
		Name   string `json:"name"`
	}
	devices := make([]safeDevice, len(catalog.Devices))
	for index, device := range catalog.Devices {
		devices[index] = safeDevice{ID: string(device.ID), Kind: string(device.Kind), Vendor: device.Vendor, Name: device.Name}
	}
	report := struct {
		Product     product.Info                    `json:"product"`
		Devices     []safeDevice                    `json:"devices"`
		Metrics     []telemetry.Descriptor          `json:"metrics"`
		Diagnostics []telemetry.ProviderDiagnostics `json:"providers"`
	}{info, devices, append([]telemetry.Descriptor(nil), catalog.Metrics...), append([]telemetry.ProviderDiagnostics(nil), diagnostics...)}
	return json.MarshalIndent(report, "", "  ")
}
