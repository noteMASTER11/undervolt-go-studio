// Command ui-snapshot renders Studio pages without opening a desktop window.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/noteMASTER11/undervolt-go-studio/internal/hardware"
	"github.com/noteMASTER11/undervolt-go-studio/internal/product"
	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
	"github.com/noteMASTER11/undervolt-go-studio/internal/ui"
	"github.com/noteMASTER11/undervolt-go-studio/internal/ui/pages"
	"github.com/noteMASTER11/undervolt-go-studio/internal/ui/viewmodel"
)

func main() {
	page := flag.String("page", "hardware", "page ID to render")
	scenario := flag.String("scenario", "", "deterministic scenario to render")
	output := flag.String("output", "ui-snapshot.png", "PNG output path")
	width := flag.Int("width", 1600, "canvas width")
	height := flag.Int("height", 1000, "canvas height")
	flag.Parse()
	done := make(chan error, 1)
	go func() { done <- render(*page, *scenario, *output, *width, *height) }()
	if err := <-done; err != nil {
		fatal(err)
	}
}

func render(page, scenario, output string, width, height int) error {
	application := test.NewApp()
	defer application.Quit()
	application.Settings().SetTheme(ui.StudioTheme())

	snapshot, err := createSnapshotPage(page, scenario)
	if err != nil {
		return err
	}
	var deactivateOnce sync.Once
	deactivate := func() { deactivateOnce.Do(func() { stopSnapshot(snapshot) }) }
	defer deactivate()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := snapshot.wait(ctx); err != nil {
		return err
	}
	// Freeze all snapshot sources before the test canvas builds widget
	// renderers. Otherwise a discovery callback can mutate RichText caches
	// concurrently with NewWindow or Capture.
	deactivate()

	var captured image.Image
	fyne.DoAndWait(func() {
		window := test.NewWindow(snapshot.object)
		defer window.Close()
		window.Resize(fyne.NewSize(float32(width), float32(height)))
		captured = window.Canvas().Capture()
	})

	file, err := os.Create(output)
	if err != nil {
		return err
	}
	if err := png.Encode(file, captured); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return nil
}

func createSnapshotPage(page, scenario string) (*snapshotPageResult, error) {
	var snapshot *snapshotPageResult
	var err error
	fyne.DoAndWait(func() { snapshot, err = snapshotPage(page, scenario) })
	return snapshot, err
}

func stopSnapshot(snapshot *snapshotPageResult) {
	fyne.DoAndWait(snapshot.deactivate)
	// Drain callbacks that were queued before the dispatchers were cancelled.
	fyne.DoAndWait(func() {})
}

type snapshotPageResult struct {
	object     fyne.CanvasObject
	deactivate func()
	wait       func(context.Context) error
	tune       *viewmodel.Tune
}

func snapshotPage(page, scenario string) (*snapshotPageResult, error) {
	if scenario == "intel-275hx" {
		switch page {
		case "overview", "monitor", "hardware":
			return intel275HXTelemetrySnapshotPage(page)
		case "tune":
			return intel275HXSnapshotPage(), nil
		default:
			return nil, fmt.Errorf("scenario %q does not support page %q", scenario, page)
		}
	}
	if scenario != "" {
		return nil, fmt.Errorf("unknown snapshot scenario %q", scenario)
	}

	scheduler := telemetry.NewScheduler(nil, telemetry.SchedulerOptions{})
	shell := ui.NewShell(product.Current("dev"), scheduler)
	if err := shell.Select(page); err != nil {
		shell.Deactivate()
		return nil, err
	}
	shell.SetStatus("Headless UI preview")
	return &snapshotPageResult{
		object:     shell.Object(),
		deactivate: shell.Deactivate,
		wait:       func(context.Context) error { return nil },
	}, nil
}

func intel275HXTelemetrySnapshotPage(page string) (*snapshotPageResult, error) {
	provider := newIntel275HXTelemetrySnapshotProvider()
	rendered := make(chan struct{})
	var renderedOnce sync.Once
	afterRender := func(state viewmodel.MonitorState) {
		if stateHasSnapshotTimeline(state) {
			renderedOnce.Do(func() { close(rendered) })
		}
	}
	scheduler := telemetry.NewScheduler([]telemetry.Provider{provider}, telemetry.SchedulerOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	catalog, err := scheduler.Discover(ctx)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("discover Intel 275HX snapshot telemetry: %w", err)
	}
	shell := ui.NewSnapshotShell(product.Current("snapshot"), scheduler, intel275HXHardwareSummary(), afterRender)
	shell.SetCatalog(catalog)
	shell.SetStatus("Intel Core Ultra 9 275HX · deterministic preview")
	if err := shell.Select(page); err != nil {
		cancel()
		shell.Deactivate()
		return nil, err
	}
	scheduler.Start(ctx)
	return &snapshotPageResult{
		object: shell.Object(),
		deactivate: func() {
			cancel()
			shell.Deactivate()
		},
		wait: func(waitCtx context.Context) error {
			if page == "hardware" {
				if err := shell.WaitForHardwareSummary(waitCtx); err != nil {
					return fmt.Errorf("wait for Intel 275HX hardware summary: %w", err)
				}
			} else {
				select {
				case <-rendered:
				case <-waitCtx.Done():
					return fmt.Errorf("wait for rendered Intel 275HX telemetry: %w", waitCtx.Err())
				}
			}
			fyne.DoAndWait(func() {})
			return nil
		},
	}, nil
}

func stateHasSnapshotTimeline(state viewmodel.MonitorState) bool {
	if len(intel275HXSnapshotTimeline) == 0 {
		return false
	}
	final := intel275HXSnapshotTimeline[len(intel275HXSnapshotTimeline)-1]
	for _, sample := range state.Current {
		if sample.Timestamp.Equal(final) {
			return true
		}
	}
	return false
}

func intel275HXHardwareSummary() pages.HardwareSummary {
	return pages.HardwareSummary{
		Machine:       "Studio validation workstation",
		OS:            "Linux",
		Kernel:        "Linux snapshot",
		CPU:           "Intel Core Ultra 9 275HX",
		CPUDetails:    []string{"24 cores · 24 logical processors"},
		Graphics:      []string{"Intel Arc Graphics"},
		Memory:        "32 GiB",
		MemoryDetails: []string{"12 GiB used · 20 GiB available"},
		Storage:       []string{"NVMe · 1.0 TiB"},
	}
}

type intel275HXTelemetrySnapshotProvider struct {
	sampled chan struct{}
	once    sync.Once
	samples atomic.Int32
}

var intel275HXSnapshotTimeline = []time.Time{
	time.Date(2099, time.January, 2, 15, 4, 5, 0, time.UTC),
	time.Date(2099, time.January, 2, 15, 4, 6, 0, time.UTC),
	time.Date(2099, time.January, 2, 15, 4, 7, 0, time.UTC),
}

func newIntel275HXTelemetrySnapshotProvider() *intel275HXTelemetrySnapshotProvider {
	return &intel275HXTelemetrySnapshotProvider{sampled: make(chan struct{})}
}

func (p *intel275HXTelemetrySnapshotProvider) ID() string { return "intel-275hx-snapshot" }

func (p *intel275HXTelemetrySnapshotProvider) Discover(context.Context) (telemetry.Catalog, error) {
	cpuID := hardware.NewDeviceID(hardware.KindCPU, "Intel", "core-ultra-9-275hx")
	gpuID := hardware.NewDeviceID(hardware.KindGPU, "Intel", "arc-graphics")
	return telemetry.Catalog{
		Devices: []hardware.Device{
			{ID: cpuID, Kind: hardware.KindCPU, Vendor: "Intel", Name: "Core Ultra 9 275HX"},
			{ID: gpuID, Kind: hardware.KindGPU, Vendor: "Intel", Name: "Arc Graphics"},
		},
		Metrics: []telemetry.Descriptor{
			{ID: "cpu.utilization", ProviderID: p.ID(), DeviceID: cpuID, Label: "CPU Utilization", Unit: "%", MinInterval: 100 * time.Millisecond},
			{ID: "cpu.package.temperature", ProviderID: p.ID(), DeviceID: cpuID, Label: "CPU Package Temperature", Unit: "°C", MinInterval: 100 * time.Millisecond},
			{ID: "cpu.pcore.frequency", ProviderID: p.ID(), DeviceID: cpuID, Label: "P-core Frequency", Unit: "MHz", MinInterval: 100 * time.Millisecond},
			{ID: "cpu.ecore.frequency", ProviderID: p.ID(), DeviceID: cpuID, Label: "E-core Frequency", Unit: "MHz", MinInterval: 100 * time.Millisecond},
		},
	}, nil
}

func (p *intel275HXTelemetrySnapshotProvider) Sample(_ context.Context, metricIDs []telemetry.MetricID) (telemetry.Frame, error) {
	index := int(p.samples.Add(1) - 1)
	if index >= len(intel275HXSnapshotTimeline) {
		index = len(intel275HXSnapshotTimeline) - 1
	}
	now := intel275HXSnapshotTimeline[index]
	values := map[telemetry.MetricID]float64{
		"cpu.utilization":         37.0,
		"cpu.package.temperature": 71.0,
		"cpu.pcore.frequency":     4380.0,
		"cpu.ecore.frequency":     3520.0,
	}
	samples := make([]telemetry.Sample, 0, len(metricIDs))
	for _, metricID := range metricIDs {
		samples = append(samples, telemetry.Sample{MetricID: metricID, Value: values[metricID], Timestamp: now, Quality: telemetry.QualityGood})
	}
	if index >= 2 {
		p.once.Do(func() { close(p.sampled) })
	}
	return telemetry.Frame{ProviderID: p.ID(), StartedAt: now, FinishedAt: now, Samples: samples}, nil
}

func (p *intel275HXTelemetrySnapshotProvider) wait(ctx context.Context) error {
	select {
	case <-p.sampled:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for Intel 275HX telemetry sample: %w", ctx.Err())
	}
}

func intel275HXSnapshotPage() *snapshotPageResult {
	view := viewmodel.NewTune(intel275HXSnapshotService{})
	rendered := make(chan struct{})
	var renderedOnce sync.Once
	dispatch := func(callback func()) {
		callback()
		state := view.State()
		if state.Phase == viewmodel.PhaseIdle && state.Capabilities.Generation == "intel-275hx-snapshot-v1" {
			renderedOnce.Do(func() { close(rendered) })
		}
	}
	tune := pages.NewTuneWithDispatcher(view, nil, telemetry.Catalog{}, dispatch)
	tune.Activate()
	return &snapshotPageResult{
		object:     tune.Object(),
		deactivate: tune.Deactivate,
		tune:       view,
		wait: func(ctx context.Context) error {
			select {
			case <-rendered:
				return nil
			case <-ctx.Done():
				return fmt.Errorf("wait for completed Tune discovery and render: %w", ctx.Err())
			}
		},
	}
}

type intel275HXSnapshotService struct{}

func (intel275HXSnapshotService) Discover(context.Context) <-chan tuning.DiscoveryResult {
	results := make(chan tuning.DiscoveryResult, 1)
	results <- tuning.DiscoveryResult{Set: tuning.CapabilitySet{
		Generation: "intel-275hx-snapshot-v1",
		MachineID:  "GenuineIntel-6-c6-2",
		Capabilities: []tuning.Capability{
			{ID: tuning.ControlPL1, Label: "Sustained power", Scope: "CPU package", Unit: tuning.UnitWatt, State: tuning.StateSupported, Current: tuning.NumericValue(44), Range: &tuning.NumericRange{Minimum: 15, Maximum: 120, Step: 1}},
			{ID: tuning.ControlPL2, Label: "Short boost power", Scope: "CPU package", Unit: tuning.UnitWatt, State: tuning.StateSupported, Current: tuning.NumericValue(44), Range: &tuning.NumericRange{Minimum: 15, Maximum: 120, Step: 1}},
			{ID: tuning.ControlEPP, Label: "Energy performance preference", Scope: "Intel P-state policy", Unit: tuning.UnitChoice, State: tuning.StateSupported, Current: tuning.ChoiceValue("balanced"), Choices: []string{"performance", "balanced", "power saver"}},
			{ID: tuning.ControlThermalLimit, Label: "Thermal ceiling", Scope: "CPU package", Unit: tuning.UnitCelsius, State: tuning.StateSupported, Current: tuning.NumericValue(95), Range: &tuning.NumericRange{Minimum: 70, Maximum: 100, Step: 1}},
			{ID: tuning.ControlRatioPCore, Label: "P-core turbo ratios", Scope: "1–8 active performance cores", Unit: tuning.UnitRatio, State: tuning.StateReadOnly, Current: tuning.VectorValue([]float64{50, 50, 49, 49, 48, 48, 47, 47}), Reason: "Writability and stock restoration are unverified"},
			{ID: tuning.ControlVoltageCore, Label: "Core voltage offset", Scope: "CPU core plane", Unit: tuning.UnitMilliVolt, State: tuning.StateReadOnly, Reason: "Voltage lock state and restoration are unverified"},
		},
	}, Complete: true}
	close(results)
	return results
}

func (intel275HXSnapshotService) Apply(context.Context, tuning.ChangeSet) (<-chan tuning.Event, error) {
	return nil, errors.New("snapshot scenarios never apply tuning changes")
}

func (intel275HXSnapshotService) Revert(context.Context) error { return nil }
func (intel275HXSnapshotService) Close(context.Context) error  { return nil }

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
