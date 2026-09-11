package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/noteMASTER11/undervolt-go-studio/internal/product"
	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
	"github.com/noteMASTER11/undervolt-go-studio/internal/ui/pages"
)

// Shell is the persistent XTU-style navigation and tuning frame.
type Shell struct {
	info      product.Info
	scheduler *telemetry.Scheduler
	navigator *LazyNavigator
	center    *fyne.Container
	root      fyne.CanvasObject

	statusLabel  *widget.Label
	applyButton  *widget.Button
	revertButton *widget.Button
}

func NewShell(info product.Info, scheduler *telemetry.Scheduler) *Shell {
	shell := &Shell{info: info, scheduler: scheduler, center: container.NewStack()}
	factories := []PageFactory{
		placeholderFactory("overview", "Overview", theme.HomeIcon(), "Hardware discovery is starting."),
		placeholderFactory("monitor", "Monitor", theme.VisibilityIcon(), "Live monitoring is not connected yet."),
		placeholderFactory("tune", "Tune", theme.SettingsIcon(), "Tuning is disabled in the read-only milestone."),
		placeholderFactory("stress", "Stress Tests", theme.MediaPlayIcon(), "Stress engines are delivered in a later milestone."),
		placeholderFactory("profiles", "Profiles", theme.StorageIcon(), "Profile editing is delivered with privileged tuning."),
		placeholderFactory("reports", "Reports", theme.DocumentIcon(), "Reports are delivered after session recording."),
		placeholderFactory("hardware", "Hardware", theme.ComputerIcon(), "Provider diagnostics are not connected yet."),
		placeholderFactory("logs", "Logs", theme.ListIcon(), "No Studio events have been recorded."),
	}
	shell.navigator = NewLazyNavigator(factories)

	navigation := container.NewVBox()
	for _, factory := range shell.navigator.Factories() {
		id := factory.ID
		button := widget.NewButtonWithIcon(factory.Label, factory.Icon, func() {
			_ = shell.Select(id)
		})
		button.Alignment = widget.ButtonAlignLeading
		navigation.Add(container.NewGridWrap(fyne.NewSize(190, 42), button))
	}
	left := container.NewBorder(
		container.NewPadded(widget.NewLabelWithStyle("WORKSPACE", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})),
		nil, nil, nil,
		container.NewPadded(navigation),
	)

	shell.statusLabel = widget.NewLabel("Discovering hardware…")
	shell.applyButton = widget.NewButton("Apply", nil)
	shell.revertButton = widget.NewButton("Revert", nil)
	shell.applyButton.Disable()
	shell.revertButton.Disable()
	right := container.NewGridWrap(fyne.NewSize(270, 520), container.NewPadded(container.NewVBox(
		widget.NewLabelWithStyle("QUICK TUNING", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewSeparator(),
		widget.NewLabel("Current profile"),
		widget.NewLabelWithStyle("Stock", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewSeparator(),
		widget.NewLabel("PL1"),
		widget.NewLabel("— unavailable"),
		widget.NewLabel("PL2"),
		widget.NewLabel("— unavailable"),
		layout.NewSpacer(),
		shell.statusLabel,
		container.NewGridWithColumns(2, shell.revertButton, shell.applyButton),
	)))

	header := container.NewPadded(container.NewBorder(
		nil, nil, nil,
		widget.NewLabel(info.Version),
		widget.NewLabelWithStyle(info.Name, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
	))
	shell.root = container.NewBorder(header, nil, left, right, shell.center)
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

func (s *Shell) Deactivate() {
	s.navigator.Deactivate()
}

func placeholderFactory(id, label string, icon fyne.Resource, message string) PageFactory {
	return PageFactory{
		ID: id, Label: label, Icon: icon,
		Create: func() Page { return pages.NewPlaceholder(id, label, message) },
	}
}
