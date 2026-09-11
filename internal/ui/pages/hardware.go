package pages

import (
	"context"
	"encoding/json"
	"errors"
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

// HardwareSummary is the host information rendered by the Hardware page.
// Snapshot callers can provide a verified summary without probing the host.
type HardwareSummary struct {
	Machine       string
	OS            string
	Kernel        string
	CPU           string
	CPUDetails    []string
	Graphics      []string
	Memory        string
	MemoryDetails []string
	Storage       []string
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
	summary func(context.Context, telemetry.Catalog) HardwareSummary

	mu              sync.Mutex
	active          bool
	stop            chan struct{}
	done            chan struct{}
	rows            []diagnosticsRow
	summaryRevision uint64
	summaryCancel   context.CancelFunc
	summaryReady    chan struct{}
	summaryLoaded   bool

	status      *widget.Label
	summaryHost *fyne.Container
	root        fyne.CanvasObject
}

func NewHardware(info product.Info, source HardwareSource, catalog telemetry.Catalog) *Hardware {
	return newHardware(info, source, catalog, readHardwareOverview)
}

// NewHardwareWithSummary creates a Hardware page that displays a supplied,
// verified summary. It is used by deterministic headless documentation views.
func NewHardwareWithSummary(info product.Info, source HardwareSource, catalog telemetry.Catalog, summary HardwareSummary) *Hardware {
	return newHardware(info, source, catalog, func(context.Context, telemetry.Catalog) HardwareSummary { return summary })
}

func newHardware(info product.Info, source HardwareSource, catalog telemetry.Catalog, summary func(context.Context, telemetry.Catalog) HardwareSummary) *Hardware {
	page := &Hardware{info: info, source: source, catalog: cloneCatalog(catalog), summary: summary}
	page.status = widget.NewLabel("Checking telemetry sources…")
	page.summaryHost = container.NewStack(container.NewCenter(widget.NewLabel("Loading hardware overview…")))
	copyButton := widget.NewButton("Copy Diagnostics", page.copyDiagnostics)
	header := container.NewBorder(nil, nil, nil, copyButton, container.NewVBox(
		widget.NewLabelWithStyle("Hardware", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("A concise overview of this computer"),
		page.status,
	))
	page.root = container.NewPadded(container.NewBorder(header, nil, nil, nil, page.summaryHost))
	page.SetCatalog(catalog)
	page.setDiagnostics(source.Diagnostics())
	return page
}

func hardwareOverviewObject(overview HardwareSummary) fyne.CanvasObject {
	body := container.NewVBox(
		container.NewGridWithColumns(2,
			hardwareCard("System", overview.Machine, overview.OS, overview.Kernel),
			hardwareCard("Processor", overview.CPU, overview.CPUDetails...),
		),
		container.NewGridWithColumns(2,
			hardwareCard("Graphics", firstOrFallback(overview.Graphics, "No graphics adapter detected"), remaining(overview.Graphics)...),
			hardwareCard("Memory", overview.Memory, overview.MemoryDetails...),
		),
		hardwareCard("Storage", firstOrFallback(overview.Storage, "No local drives detected"), remaining(overview.Storage)...),
	)
	return container.NewVScroll(body)
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
	reload := !p.summaryLoaded && p.summaryCancel == nil
	catalog := cloneCatalog(p.catalog)
	p.mu.Unlock()
	if reload {
		p.SetCatalog(catalog)
	}
	go p.refreshLoop(stop, done)
}

func (p *Hardware) Deactivate() {
	p.mu.Lock()
	p.summaryRevision++
	cancelSummary := p.summaryCancel
	p.summaryCancel = nil
	if !p.active {
		p.mu.Unlock()
		if cancelSummary != nil {
			cancelSummary()
		}
		return
	}
	p.active = false
	stop, done := p.stop, p.done
	p.stop, p.done = nil, nil
	p.mu.Unlock()
	if cancelSummary != nil {
		cancelSummary()
	}
	close(stop)
	<-done
}

func (p *Hardware) SetCatalog(catalog telemetry.Catalog) {
	p.mu.Lock()
	previousCancel := p.summaryCancel
	p.catalog = cloneCatalog(catalog)
	p.summaryRevision++
	revision := p.summaryRevision
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	p.summaryCancel = cancel
	ready := make(chan struct{})
	p.summaryReady = ready
	p.summaryLoaded = false
	p.mu.Unlock()
	if previousCancel != nil {
		previousCancel()
	}
	p.summaryHost.Objects = []fyne.CanvasObject{container.NewCenter(widget.NewLabel("Loading hardware overview…"))}
	p.summaryHost.Refresh()
	go func() {
		defer cancel()
		overview := p.summary(ctx, catalog)
		fyne.Do(func() {
			p.mu.Lock()
			current := p.summaryRevision == revision
			if current {
				p.summaryCancel = nil
				p.summaryLoaded = true
			}
			p.mu.Unlock()
			if !current {
				return
			}
			p.summaryHost.Objects = []fyne.CanvasObject{hardwareOverviewObject(overview)}
			p.summaryHost.Refresh()
			close(ready)
		})
	}()
}

// WaitForSummary waits until the current Hardware summary has reached the UI.
func (p *Hardware) WaitForSummary(ctx context.Context) error {
	p.mu.Lock()
	ready := p.summaryReady
	p.mu.Unlock()
	if ready == nil {
		return errors.New("hardware summary is not loading")
	}
	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
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
	healthy := 0
	for _, row := range rows {
		if row.Error == "" {
			healthy++
		}
	}
	if len(rows) == 0 {
		p.status.SetText("Telemetry sources will appear when monitoring starts")
		return
	}
	if healthy == len(rows) {
		p.status.SetText(fmt.Sprintf("Telemetry ready · %d sources available", healthy))
		return
	}
	p.status.SetText(fmt.Sprintf("Telemetry limited · %d of %d sources available", healthy, len(rows)))
}

func hardwareCard(title, primary string, details ...string) fyne.CanvasObject {
	content := container.NewVBox()
	primaryLabel := widget.NewLabelWithStyle(primary, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	primaryLabel.Wrapping = fyne.TextWrapWord
	content.Add(primaryLabel)
	for _, detail := range details {
		if detail == "" {
			continue
		}
		label := widget.NewLabel(detail)
		label.Wrapping = fyne.TextWrapWord
		content.Add(label)
	}
	return widget.NewCard(title, "", content)
}

func firstOrFallback(values []string, fallback string) string {
	if len(values) == 0 {
		return fallback
	}
	return values[0]
}

func remaining(values []string) []string {
	if len(values) < 2 {
		return nil
	}
	return values[1:]
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

func formatDiagnosticsRow(row diagnosticsRow) string {
	return fmt.Sprintf(
		"%s  |  %s  |  success %s  |  latency %s  |  metrics %d  |  consumers %d  |  dropped %d  |  %s",
		row.Provider, row.State, row.Success, row.Latency, row.Metrics, row.Consumers, row.Dropped, row.Error,
	)
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
