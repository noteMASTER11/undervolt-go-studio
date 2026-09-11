// Command ui-snapshot renders Studio pages without opening a desktop window.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image/png"
	"os"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

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

	object, deactivate, err := snapshotPage(page, scenario)
	if err != nil {
		return err
	}
	defer deactivate()

	window := test.NewWindow(object)
	defer window.Close()
	window.Resize(fyne.NewSize(float32(width), float32(height)))
	time.Sleep(2 * time.Second)

	file, err := os.Create(output)
	if err != nil {
		return err
	}
	if err := png.Encode(file, window.Canvas().Capture()); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return nil
}

func snapshotPage(page, scenario string) (fyne.CanvasObject, func(), error) {
	if scenario == "intel-275hx" {
		if page != "tune" {
			return nil, nil, fmt.Errorf("scenario %q only supports the Tune page", scenario)
		}
		view := viewmodel.NewTune(intel275HXSnapshotService{})
		tune := pages.NewTuneWithDispatcher(view, nil, telemetry.Catalog{}, func(callback func()) { callback() })
		tune.Activate()
		return tune.Object(), tune.Deactivate, nil
	}
	if scenario != "" {
		return nil, nil, fmt.Errorf("unknown snapshot scenario %q", scenario)
	}

	scheduler := telemetry.NewScheduler(nil, telemetry.SchedulerOptions{})
	shell := ui.NewShell(product.Current("dev"), scheduler)
	if err := shell.Select(page); err != nil {
		shell.Deactivate()
		return nil, nil, err
	}
	shell.SetStatus("Headless UI preview")
	return shell.Object(), shell.Deactivate, nil
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
			{ID: tuning.ControlRatioPCore, Label: "P-core turbo ratios", Scope: "1–8 active performance cores", Unit: tuning.UnitRatio, State: tuning.StateSupported, Current: tuning.VectorValue([]float64{50, 50, 49, 49, 48, 48, 47, 47})},
			{ID: tuning.ControlVoltageCore, Label: "Core voltage offset", Scope: "CPU core plane", Unit: tuning.UnitMilliVolt, State: tuning.StateFirmwareLocked, Current: tuning.NumericValue(0), Range: &tuning.NumericRange{Minimum: -100, Maximum: 0, Step: 1}, Reason: "BIOS firmware lock is enabled"},
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
