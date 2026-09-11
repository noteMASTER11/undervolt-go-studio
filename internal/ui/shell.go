package ui

import (
	"context"
	"errors"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/noteMASTER11/undervolt-go-studio/internal/events"
	"github.com/noteMASTER11/undervolt-go-studio/internal/product"
	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
	"github.com/noteMASTER11/undervolt-go-studio/internal/ui/pages"
	"github.com/noteMASTER11/undervolt-go-studio/internal/ui/viewmodel"
)

// Shell is the persistent XTU-style navigation and tuning frame.
type Shell struct {
	info      product.Info
	scheduler *telemetry.Scheduler
	catalog   telemetry.Catalog
	navigator *LazyNavigator
	events    *events.Store
	tune      *viewmodel.Tune
	center    *fyne.Container
	root      fyne.CanvasObject

	statusLabel *widget.Label
	hardware    *pages.Hardware
}

func NewShell(info product.Info, scheduler *telemetry.Scheduler) *Shell {
	eventStore := events.NewStore(0)
	return newShell(info, scheduler, eventStore, newDesktopTuneService(info.Version, eventStore), fyne.Do)
}

// NewShellWithHardwareSummary creates a shell whose Hardware page displays a
// supplied summary. It supports deterministic headless documentation views.
func NewShellWithHardwareSummary(info product.Info, scheduler *telemetry.Scheduler, summary pages.HardwareSummary) *Shell {
	eventStore := events.NewStore(0)
	shell := newShell(info, scheduler, eventStore, newDesktopTuneService(info.Version, eventStore), fyne.Do)
	for index, factory := range shell.navigator.factories {
		if factory.ID != "hardware" {
			continue
		}
		shell.navigator.factories[index].Create = func() Page {
			shell.hardware = pages.NewHardwareWithSummary(info, scheduler, shell.catalog, summary)
			return shell.hardware
		}
		shell.navigator.byID["hardware"] = shell.navigator.factories[index]
		break
	}
	return shell
}

// NewSnapshotShell configures telemetry pages to report after their UI callback
// has rendered the latest state. It is only used by deterministic headless views.
func NewSnapshotShell(info product.Info, scheduler *telemetry.Scheduler, summary pages.HardwareSummary, afterRender func(viewmodel.MonitorState)) *Shell {
	shell := NewShellWithHardwareSummary(info, scheduler, summary)
	shell.navigator.Deactivate()
	delete(shell.navigator.pages, "overview")
	source := viewmodel.SchedulerSource{Scheduler: scheduler}
	for index, factory := range shell.navigator.factories {
		switch factory.ID {
		case "overview":
			shell.navigator.factories[index].Create = func() Page { return pages.NewOverviewWithRenderCallback(source, shell.catalog, afterRender) }
		case "monitor":
			shell.navigator.factories[index].Create = func() Page { return pages.NewMonitorWithRenderCallback(source, shell.catalog, afterRender) }
		default:
			continue
		}
		shell.navigator.byID[factory.ID] = shell.navigator.factories[index]
	}
	return shell
}

func newShell(info product.Info, scheduler *telemetry.Scheduler, eventStore *events.Store, tuneService viewmodel.TuneService, dispatch func(func())) *Shell {
	shell := &Shell{info: info, scheduler: scheduler, center: container.NewStack(), events: eventStore}
	source := viewmodel.SchedulerSource{Scheduler: scheduler}
	shell.tune = viewmodel.NewTune(tuneService)
	factories := []PageFactory{
		{ID: "overview", Label: "Overview", Icon: theme.HomeIcon(), Create: func() Page { return pages.NewOverview(source, shell.catalog) }},
		{ID: "monitor", Label: "Monitor", Icon: theme.VisibilityIcon(), Create: func() Page { return pages.NewMonitor(source, shell.catalog) }},
		{ID: "tune", Label: "Tune", Icon: theme.SettingsIcon(), Create: func() Page {
			return pages.NewTuneWithDispatcher(shell.tune, source, shell.catalog, dispatch)
		}},
		placeholderFactory("stress", "Stress Tests", theme.MediaPlayIcon(), "Stress engines are delivered in a later milestone."),
		placeholderFactory("profiles", "Profiles", theme.StorageIcon(), "Profile editing is delivered with privileged tuning."),
		placeholderFactory("reports", "Reports", theme.DocumentIcon(), "Reports are delivered after session recording."),
		{ID: "hardware", Label: "Hardware", Icon: theme.ComputerIcon(), Create: func() Page { return pages.NewHardware(info, scheduler, shell.catalog) }},
		{ID: "logs", Label: "Logs", Icon: theme.ListIcon(), Create: func() Page { return pages.NewLogs(shell.events) }},
	}
	shell.navigator = NewLazyNavigator(factories)

	navigation := container.NewVBox()
	for _, factory := range shell.navigator.Factories() {
		id := factory.ID
		button := widget.NewButtonWithIcon(factory.Label, factory.Icon, func() {
			_ = shell.Select(id)
		})
		button.Alignment = widget.ButtonAlignLeading
		navigation.Add(container.NewGridWrap(fyne.NewSize(180, 42), button))
	}
	left := container.NewBorder(
		container.NewPadded(widget.NewLabelWithStyle("WORKSPACE", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})),
		nil, nil, nil,
		container.NewPadded(navigation),
	)

	shell.statusLabel = widget.NewLabel("Discovering hardware…")
	header := container.NewPadded(container.NewBorder(
		nil, nil, nil,
		container.NewHBox(shell.statusLabel, widget.NewSeparator(), widget.NewLabel(info.Version)),
		widget.NewLabelWithStyle(info.Name, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
	))
	shell.root = container.NewBorder(header, nil, left, nil, shell.center)
	_ = shell.Select("overview")
	return shell
}

func (s *Shell) Object() fyne.CanvasObject {
	return s.root
}

func (s *Shell) Select(id string) error {
	page, err := s.navigator.Select(id)
	if err != nil {
		return err
	}
	s.center.Objects = []fyne.CanvasObject{page.Object()}
	s.center.Refresh()
	return nil
}

func (s *Shell) SetStatus(status string) {
	s.statusLabel.SetText(status)
}

func (s *Shell) SetCatalog(catalog telemetry.Catalog) {
	s.catalog = telemetry.Catalog{
		Devices: append(catalog.Devices[:0:0], catalog.Devices...),
		Metrics: append(catalog.Metrics[:0:0], catalog.Metrics...),
	}
	for _, page := range s.navigator.pages {
		if livePage, ok := page.(interface{ SetCatalog(telemetry.Catalog) }); ok {
			livePage.SetCatalog(s.catalog)
		}
	}
}

func (s *Shell) Deactivate() {
	s.navigator.Deactivate()
}

func (s *Shell) CloseTune() error {
	if s.tune == nil {
		return nil
	}
	return s.tune.Close()
}

// WaitForHardwareSummary waits for the selected Hardware page to render its summary.
func (s *Shell) WaitForHardwareSummary(ctx context.Context) error {
	if s.hardware == nil {
		return errors.New("hardware page is not selected")
	}
	return s.hardware.WaitForSummary(ctx)
}

func placeholderFactory(id, label string, icon fyne.Resource, message string) PageFactory {
	return PageFactory{
		ID: id, Label: label, Icon: icon,
		Create: func() Page { return pages.NewPlaceholder(id, label, message) },
	}
}
