package pages

import (
	"context"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
	"github.com/noteMASTER11/undervolt-go-studio/internal/ui/viewmodel"
)

func TestTunePageUsesWorkbenchGroupsAndPendingRail(t *testing.T) {
	application := test.NewApp()
	defer application.Quit()
	viewModel := viewmodel.NewTune(emptyTuneService{})
	page := NewTuneWithDispatcher(viewModel, nil, telemetry.Catalog{}, immediateDispatch)
	for _, label := range []string{"Power limits", "Thermal & voltage", "Core ratios", "Pending changes"} {
		if !page.HasSection(label) {
			t.Fatalf("missing %q", label)
		}
	}
	if title := page.liveCards["frequency"].Object().(*widget.Card).Title; title != "CPU maximum" {
		t.Fatalf("unclassified frequency mislabeled %q", title)
	}
}

func TestTuneRendersVerifiedResultsAndRecoveryActions(t *testing.T) {
	application := test.NewApp()
	defer application.Quit()
	page := NewTuneWithDispatcher(viewmodel.NewTune(emptyTuneService{}), nil, telemetry.Catalog{}, immediateDispatch)
	state := viewmodel.TuneState{Phase: viewmodel.PhaseActive, SessionActive: true, Capabilities: capabilitySetForPage(), Pending: []tuning.Change{{ID: tuning.ControlPL1, Requested: tuning.NumericValue(40)}}, Effective: map[tuning.ControlID]tuning.Value{tuning.ControlPL1: tuning.NumericValue(39.5)}}
	page.render(state)
	if !strings.Contains(page.results.Text, "Requested 40.00") || !strings.Contains(page.results.Text, "Verified 39.50") {
		t.Fatalf("requested/effective distinction missing: %q", page.results.Text)
	}
	captureTuneOutcome(t, page, "tune-active.png")
	state.Phase = viewmodel.PhaseRollbackIncomplete
	state.Remaining = state.Effective
	state.LastError = "Stock restoration incomplete"
	page.render(state)
	if !page.recovery.Visible() || page.retry.Disabled() || page.reboot.Disabled() || !strings.Contains(page.results.Text, "Verified remaining") {
		t.Fatal("incomplete rollback lost persistent recovery controls")
	}
	captureTuneOutcome(t, page, "tune-rollback-incomplete.png")
	page.Object().Resize(fyne.NewSize(1600, 1000))
	if page.recovery.Position().Y < page.results.Position().Y+page.results.Size().Height {
		t.Fatal("recovery actions overlap verified outcome instead of following it")
	}
}

// Optional headless visual QA uses only Fyne's in-memory test canvas.
func captureTuneOutcome(t *testing.T, page *Tune, name string) {
	t.Helper()
	directory := os.Getenv("STUDIO_TUNE_QA_DIR")
	if directory == "" {
		return
	}
	window := test.NewWindow(page.Object())
	defer window.Close()
	window.Resize(fyne.NewSize(1600, 1000))
	file, err := os.Create(filepath.Join(directory, name))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, window.Canvas().Capture()); err != nil {
		t.Fatal(err)
	}
}

func TestTunePageSectionsDoNotOverlapAfterAsyncDiscovery(t *testing.T) {
	application := test.NewApp()
	defer application.Quit()
	viewModel := viewmodel.NewTune(staticTuneService{})
	dispatcher := &queuedUIDispatcher{}
	page := NewTuneWithDispatcher(viewModel, nil, telemetry.Catalog{}, dispatcher.Dispatch)
	page.Object().Resize(fyne.NewSize(1500, 900))
	page.Activate()
	deadline := time.Now().Add(time.Second)
	for viewModel.State().Capabilities.Generation != "g" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if viewModel.State().Capabilities.Generation != "g" {
		t.Fatal("capability discovery did not complete")
	}
	dispatcher.Drain()
	for index := 1; index < len(page.workbench.Objects); index++ {
		previous := page.workbench.Objects[index-1]
		current := page.workbench.Objects[index]
		if previous.Position().Y+previous.Size().Height > current.Position().Y {
			t.Fatalf("sections %d and %d overlap", index-1, index)
		}
	}
	page.Deactivate()
	dispatcher.Drain()
}

func TestTunePageSubscribesToWorkbenchTelemetryOnlyWhileVisible(t *testing.T) {
	application := test.NewApp()
	defer application.Quit()
	source := &pageSource{}
	catalog := telemetry.Catalog{Metrics: []telemetry.Descriptor{
		{ID: "hwmon.package.temperature", Label: "CPU Package", Unit: "°C"},
		{ID: "rapl.package.power", Label: "Package Power", Unit: "W"},
		{ID: "cpu.0.frequency", Label: "CPU 0 Frequency", Unit: "MHz"},
		{ID: "cpu.1.frequency", Label: "CPU 1 Frequency", Unit: "MHz"},
		{ID: "gpu.0.frequency", Label: "GPU Frequency", Unit: "MHz"},
	}}
	dispatcher := &queuedUIDispatcher{}
	page := NewTuneWithDispatcher(viewmodel.NewTune(emptyTuneService{}), source, catalog, dispatcher.Dispatch)
	if source.calls != 0 {
		t.Fatal("Tune subscribed to live telemetry before activation")
	}
	page.Activate()
	if source.calls != 1 {
		t.Fatalf("subscriptions = %d, want 1", source.calls)
	}
	want := []telemetry.MetricID{"hwmon.package.temperature", "rapl.package.power", "cpu.0.frequency", "cpu.1.frequency"}
	got := source.subscriptions[0].MetricIDs
	if len(got) != len(want) {
		t.Fatalf("metric IDs = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("metric IDs = %v, want %v", got, want)
		}
	}
	page.Deactivate()
	dispatcher.Drain()
	if source.handle.closed != 1 {
		t.Fatalf("telemetry closes = %d, want 1", source.handle.closed)
	}
}

func TestTuneTelemetrySnapshotUsesFastestCPUFrequency(t *testing.T) {
	metrics := tuneTelemetryMetrics{
		temperature: "temperature",
		power:       "power",
		frequencies: []telemetry.MetricID{"cpu.0", "cpu.1"},
	}
	state := viewmodel.MonitorState{Current: map[telemetry.MetricID]telemetry.Sample{
		"temperature": {MetricID: "temperature", Value: 78, Quality: telemetry.QualityGood},
		"power":       {MetricID: "power", Value: 37.5, Quality: telemetry.QualityGood},
		"cpu.0":       {MetricID: "cpu.0", Value: 3200, Quality: telemetry.QualityGood},
		"cpu.1":       {MetricID: "cpu.1", Value: 5100, Quality: telemetry.QualityGood},
	}}
	snapshot := tuneTelemetrySnapshot(state, metrics)
	if snapshot.temperature.Value != 78 || snapshot.power.Value != 37.5 || snapshot.frequency.Value != 5100 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestTuneRenderPreservesEditorsWhileStaging(t *testing.T) {
	application := test.NewApp()
	defer application.Quit()
	page := NewTuneWithDispatcher(viewmodel.NewTune(emptyTuneService{}), nil, telemetry.Catalog{}, immediateDispatch)
	capabilities := capabilitySetForPage()
	page.render(viewmodel.TuneState{Phase: viewmodel.PhaseIdle, Capabilities: capabilities})
	first := page.controls[tuning.ControlPL1]
	page.render(viewmodel.TuneState{
		Phase: viewmodel.PhaseStaged, Capabilities: capabilities,
		Pending: []tuning.Change{{ID: tuning.ControlPL1, Requested: tuning.NumericValue(40)}},
	})
	if page.controls[tuning.ControlPL1] != first {
		t.Fatal("staging rebuilt the active editor")
	}
}

func TestTuneRenderShowsTerminalEmptyDiscovery(t *testing.T) {
	application := test.NewApp()
	defer application.Quit()
	page := NewTuneWithDispatcher(viewmodel.NewTune(emptyTuneService{}), nil, telemetry.Catalog{}, immediateDispatch)
	page.render(viewmodel.TuneState{Phase: viewmodel.PhaseIdle})
	if !strings.Contains(page.emptyMessage.Text, "No adjustable") {
		t.Fatalf("empty discovery text = %q", page.emptyMessage.Text)
	}
}

func TestReviewRowShowsRequestedAndNormalizedValues(t *testing.T) {
	row := formatReviewRow(viewmodel.ReviewRow{
		Label: "Sustained power", Stock: tuning.NumericValue(44), Requested: tuning.NumericValue(43.96),
		Normalized: tuning.NumericValue(44), Adjusted: true,
	})
	for _, text := range []string{"Stock 44.00", "Requested 43.96", "Expected 44.00", "adjusted"} {
		if !strings.Contains(row, text) {
			t.Fatalf("review row %q misses %q", row, text)
		}
	}
}

func TestTuneRenderDisablesReviewForInvalidPendingSet(t *testing.T) {
	state := viewmodel.TuneState{
		Phase: viewmodel.PhaseStaged, Capabilities: capabilitySetForPage(),
		Pending: []tuning.Change{{ID: tuning.ControlPL1, Requested: tuning.ChoiceValue("invalid")}},
	}
	if canReview(state) {
		t.Fatal("invalid pending set enabled review")
	}
	state.PendingValid = true
	if !canReview(state) {
		t.Fatal("valid pending set did not enable review")
	}
}

func TestTunePageQueuesBackgroundDiscoveryAndDropsItAfterDeactivate(t *testing.T) {
	application := test.NewApp()
	defer application.Quit()
	var mu sync.Mutex
	var queued []func()
	dispatch := func(callback func()) {
		mu.Lock()
		queued = append(queued, callback)
		mu.Unlock()
	}
	page := NewTuneWithDispatcher(viewmodel.NewTune(staticTuneService{}), nil, telemetry.Catalog{}, dispatch)
	page.Activate()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		count := len(queued)
		mu.Unlock()
		if count > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if page.controls[tuning.ControlPL1] != nil {
		t.Fatal("background discovery mutated widgets before UI dispatch")
	}
	page.Deactivate()
	mu.Lock()
	callbacks := append([]func(){}, queued...)
	mu.Unlock()
	for _, callback := range callbacks {
		callback()
	}
	if page.controls[tuning.ControlPL1] != nil {
		t.Fatal("queued discovery rendered after page deactivation")
	}
}

func capabilitySetForPage() tuning.CapabilitySet {
	return tuning.CapabilitySet{Generation: "g", MachineID: "cpu", Capabilities: []tuning.Capability{{
		ID: tuning.ControlPL1, Label: "PL1", Unit: tuning.UnitWatt, State: tuning.StateSupported,
		Current: tuning.NumericValue(44), Range: &tuning.NumericRange{Minimum: 15, Maximum: 55, Step: 1},
	}}}
}

type emptyTuneService struct{}

type staticTuneService struct{ emptyTuneService }

func immediateDispatch(callback func()) { callback() }

type queuedUIDispatcher struct {
	mu        sync.Mutex
	callbacks []func()
}

func (dispatcher *queuedUIDispatcher) Dispatch(callback func()) {
	dispatcher.mu.Lock()
	dispatcher.callbacks = append(dispatcher.callbacks, callback)
	dispatcher.mu.Unlock()
}

func (dispatcher *queuedUIDispatcher) Drain() {
	for {
		dispatcher.mu.Lock()
		if len(dispatcher.callbacks) == 0 {
			dispatcher.mu.Unlock()
			return
		}
		callbacks := dispatcher.callbacks
		dispatcher.callbacks = nil
		dispatcher.mu.Unlock()
		for _, callback := range callbacks {
			callback()
		}
	}
}

func (staticTuneService) Discover(context.Context) <-chan tuning.DiscoveryResult {
	results := make(chan tuning.DiscoveryResult, 1)
	results <- tuning.DiscoveryResult{Complete: true, Set: tuning.CapabilitySet{Generation: "g", MachineID: "cpu", Capabilities: []tuning.Capability{
		{ID: tuning.ControlPL1, Label: "PL1", Unit: tuning.UnitWatt, State: tuning.StateSupported, Current: tuning.NumericValue(44), Range: &tuning.NumericRange{Minimum: 15, Maximum: 55, Step: 1}},
		{ID: tuning.ControlThermalLimit, Label: "Thermal", Unit: tuning.UnitCelsius, State: tuning.StateSupported, Current: tuning.NumericValue(95), Range: &tuning.NumericRange{Minimum: 70, Maximum: 105, Step: 1}},
	}}}
	close(results)
	return results
}

func (emptyTuneService) Discover(context.Context) <-chan tuning.DiscoveryResult {
	result := make(chan tuning.DiscoveryResult)
	close(result)
	return result
}
func (emptyTuneService) Apply(context.Context, tuning.ChangeSet) (<-chan tuning.Event, error) {
	events := make(chan tuning.Event)
	close(events)
	return events, nil
}
func (emptyTuneService) Revert(context.Context) error { return nil }
func (emptyTuneService) Close(context.Context) error  { return nil }
