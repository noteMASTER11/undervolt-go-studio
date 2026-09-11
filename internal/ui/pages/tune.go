package pages

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
	"github.com/noteMASTER11/undervolt-go-studio/internal/ui/components"
	"github.com/noteMASTER11/undervolt-go-studio/internal/ui/viewmodel"
)

type tuneTelemetryMetrics struct {
	temperature telemetry.MetricID
	power       telemetry.MetricID
	frequencies []telemetry.MetricID
}

type tuneTelemetryValues struct {
	temperature telemetry.Sample
	power       telemetry.Sample
	frequency   telemetry.Sample
}

type Tune struct {
	viewModel *viewmodel.Tune
	live      *viewmodel.Monitor
	sections  map[string]bool
	metrics   tuneTelemetryMetrics
	liveCards map[string]*components.MetricCard
	controls  map[tuning.ControlID]*components.TuneControl

	status             *widget.Label
	results            *widget.Label
	recovery           *fyne.Container
	retry              *widget.Button
	reboot             *widget.Button
	emptyMessage       *widget.Label
	power              *fyne.Container
	thermal            *fyne.Container
	ratios             *fyne.Container
	workbench          *fyne.Container
	pending            *components.PendingRail
	railContent        *fyne.Container
	controlsGeneration string
	controlsBuilt      bool
	controlsLocked     bool
	hadPending         bool
	dispatcher         *components.LatestDispatcher[viewmodel.TuneState]
	liveUpdates        *components.LatestDispatcher[viewmodel.MonitorState]
	root               fyne.CanvasObject
}

func NewTune(viewModel *viewmodel.Tune, source viewmodel.SubscriptionSource, catalog telemetry.Catalog) *Tune {
	return NewTuneWithDispatcher(viewModel, source, catalog, fyne.Do)
}

func NewTuneWithDispatcher(viewModel *viewmodel.Tune, source viewmodel.SubscriptionSource, catalog telemetry.Catalog, dispatch func(func())) *Tune {
	page := &Tune{
		viewModel: viewModel,
		sections:  map[string]bool{"Power limits": true, "Thermal & voltage": true, "Core ratios": true, "Pending changes": true},
		controls:  make(map[tuning.ControlID]*components.TuneControl),
		liveCards: map[string]*components.MetricCard{
			"temperature": components.NewMetricCard("Package temperature", "°C"),
			"power":       components.NewMetricCard("Package power", "W"),
			"frequency":   components.NewMetricCard("CPU maximum", "MHz"),
		},
		status:       widget.NewLabel("Open Tune to discover available controls"),
		emptyMessage: widget.NewLabel("Discovering available controls…"),
		power:        container.NewVBox(),
		thermal:      container.NewVBox(),
		ratios:       container.NewVBox(),
	}
	if source != nil {
		page.live = viewmodel.NewMonitor(source, 250*time.Millisecond)
		page.liveUpdates = components.NewLatestDispatcher(dispatch, page.renderTelemetry)
	}
	page.pending = components.NewPendingRail(page.viewModel.Reset, page.showReview, func() {
		go func() { _ = page.viewModel.Revert(context.Background()) }()
	})
	page.results = widget.NewLabel("")
	page.results.Wrapping = fyne.TextWrapWord
	page.retry = widget.NewButton("Retry rollback / recovery", func() { go func() { _ = page.viewModel.Revert(context.Background()) }() })
	page.reboot = widget.NewButton("Reboot to reset", page.showRebootGuidance)
	rebootGuide := widget.NewLabel("Save your work, then restart Linux. A hard lock requires a power cycle.")
	rebootGuide.Wrapping = fyne.TextWrapWord
	page.recovery = container.NewVBox(page.retry, page.reboot, rebootGuide)
	page.dispatcher = components.NewLatestDispatcher(dispatch, page.render)

	header := container.NewVBox(
		container.NewBorder(nil, nil, nil, page.status,
			container.NewVBox(
				widget.NewLabelWithStyle("XTU Workbench", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
				widget.NewLabel("Temporary tuning with verified read-back and automatic rollback"),
			),
		),
		container.NewGridWithColumns(3,
			page.liveCards["temperature"].Object(),
			page.liveCards["power"].Object(),
			page.liveCards["frequency"].Object(),
		),
	)
	page.workbench = container.NewVBox(
		sectionCard("Power limits", "Sustained, short boost, turbo window, and energy policy", page.power),
		sectionCard("Thermal & voltage", "Thermal ceiling and conservative voltage offsets", page.thermal),
		sectionCard("Core ratios", "Per-active-core turbo ratio limits", page.ratios),
	)
	page.railContent = container.NewVBox(page.pending.Object(), page.results, page.recovery)
	right := container.NewGridWrap(fyne.NewSize(350, 650), page.railContent)
	page.root = container.NewPadded(container.NewBorder(header, nil, nil, right, container.NewVScroll(page.workbench)))
	page.render(viewModel.State())
	page.SetCatalog(catalog)
	return page
}

func (page *Tune) ID() string                   { return "tune" }
func (page *Tune) Object() fyne.CanvasObject    { return page.root }
func (page *Tune) HasSection(label string) bool { return page.sections[label] }

func (page *Tune) Activate() {
	page.dispatcher.Resume()
	page.viewModel.SetListener(page.dispatcher.Submit)
	page.viewModel.Activate()
	if page.live != nil {
		page.liveUpdates.Resume()
		page.live.SetListener(page.liveUpdates.Submit)
		page.live.Activate()
	}
}

func (page *Tune) Deactivate() {
	page.viewModel.SetListener(nil)
	page.dispatcher.Cancel()
	page.viewModel.Deactivate()
	if page.live != nil {
		page.live.SetListener(nil)
		page.liveUpdates.Cancel()
		page.live.Deactivate()
	}
}

func (page *Tune) SetCatalog(catalog telemetry.Catalog) {
	page.metrics = classifyTuneTelemetry(catalog)
	if page.live != nil {
		page.live.SetMetricIDs(tuneTelemetryIDs(page.metrics))
	}
	page.renderTelemetry(viewmodel.MonitorState{})
}

func (page *Tune) render(state viewmodel.TuneState) {
	page.status.SetText(phaseLabel(state.Phase, state.LastError))
	page.results.SetText(outcomeText(state))
	if page.results.Text == "" {
		page.results.Hide()
	} else {
		page.results.Show()
	}
	if state.Phase == viewmodel.PhaseRollbackIncomplete {
		page.status.SetText("Rollback incomplete · attention required")
		page.recovery.Show()
		page.retry.Enable()
	} else {
		page.recovery.Hide()
	}
	locked := state.SessionActive || state.Phase == viewmodel.PhaseAuthorizing || state.Phase == viewmodel.PhaseApplying || state.Phase == viewmodel.PhaseRollingBack
	rebuild := !page.controlsBuilt || page.controlsGeneration != state.Capabilities.Generation || page.controlsLocked != locked || (page.hadPending && len(state.Pending) == 0)
	if rebuild {
		page.rebuildControls(state, locked)
	}
	page.hadPending = len(state.Pending) > 0
	page.pending.SetChanges(state.Pending, state.Capabilities)
	page.pending.SetReviewEnabled(canReview(state))
	page.pending.SetSessionActive(state.SessionActive)
	if page.railContent != nil {
		page.railContent.Refresh()
	}
	if page.root != nil {
		page.root.Refresh()
	}
}

func outcomeText(state viewmodel.TuneState) string {
	labels := make(map[tuning.ControlID]string)
	units := make(map[tuning.ControlID]tuning.Unit)
	requested := make(map[tuning.ControlID]tuning.Value)
	for _, cap := range state.Capabilities.Capabilities {
		labels[cap.ID] = cap.Label
		units[cap.ID] = cap.Unit
	}
	for _, change := range state.Pending {
		requested[change.ID] = change.Requested
	}
	values := state.Effective
	if state.Phase == viewmodel.PhaseRollbackIncomplete {
		values = state.Remaining
	}
	ids := make([]tuning.ControlID, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var rows []string
	if state.Phase == viewmodel.PhaseRollbackIncomplete {
		message := state.LastError
		if message == "" {
			message = "Stock restoration is incomplete."
		}
		rows = append(rows, message)
	}
	for _, id := range ids {
		label := labels[id]
		if label == "" {
			label = string(id)
		}
		if state.Phase == viewmodel.PhaseRollbackIncomplete {
			rows = append(rows, fmt.Sprintf("%s · Verified remaining %s %s", label, valueText(values[id]), units[id]))
		} else {
			rows = append(rows, fmt.Sprintf("%s\nRequested %s %s · Verified %s %s", label, valueText(requested[id]), units[id], valueText(values[id]), units[id]))
		}
	}
	for _, id := range state.Unverified {
		label := labels[id]
		if label == "" {
			label = string(id)
		}
		rows = append(rows, label+" · remaining value could not be verified")
	}
	return strings.Join(rows, "\n\n")
}

func canReview(state viewmodel.TuneState) bool {
	return state.Phase == viewmodel.PhaseStaged && state.PendingValid && !state.SessionActive
}

func (page *Tune) rebuildControls(state viewmodel.TuneState, locked bool) {
	page.power.RemoveAll()
	page.thermal.RemoveAll()
	page.ratios.RemoveAll()
	page.controls = make(map[tuning.ControlID]*components.TuneControl)
	if len(state.Capabilities.Capabilities) == 0 {
		if state.Phase == viewmodel.PhaseDiscovering {
			page.emptyMessage.SetText("Discovering available controls…")
			page.power.Add(container.NewCenter(widget.NewProgressBarInfinite()))
		} else {
			page.emptyMessage.SetText("No adjustable Intel controls were reported on this system.")
			page.power.Add(page.emptyMessage)
		}
		page.thermal.Add(widget.NewLabel("No thermal or voltage controls were reported."))
		page.ratios.Add(widget.NewLabel("No ratio controls were reported."))
	} else {
		pending := make(map[tuning.ControlID]tuning.Value, len(state.Pending))
		for _, change := range state.Pending {
			pending[change.ID] = change.Requested
		}
		for _, capability := range state.Capabilities.Capabilities {
			if value, exists := pending[capability.ID]; exists {
				capability.Current = value
			}
			var onStage func(tuning.ControlID, tuning.Value)
			if !locked {
				onStage = func(id tuning.ControlID, value tuning.Value) { _ = page.viewModel.Stage(id, value) }
			}
			control := components.NewTuneControl(capability, onStage)
			page.controls[capability.ID] = control
			switch capability.ID {
			case tuning.ControlPL1, tuning.ControlPL2, tuning.ControlTau, tuning.ControlEPP:
				page.power.Add(control.Object())
			case tuning.ControlThermalLimit, tuning.ControlVoltageCore, tuning.ControlVoltageCache:
				page.thermal.Add(control.Object())
			case tuning.ControlRatioPCore, tuning.ControlRatioECore:
				page.ratios.Add(control.Object())
			}
		}
	}
	page.power.Refresh()
	page.thermal.Refresh()
	page.ratios.Refresh()
	if page.workbench != nil {
		page.workbench.Refresh()
	}
	if page.root != nil {
		page.root.Refresh()
	}
	page.controlsGeneration = state.Capabilities.Generation
	page.controlsLocked = locked
	page.controlsBuilt = true
}

func (page *Tune) showReview() {
	rows, err := page.viewModel.Review()
	if err != nil {
		page.status.SetText(err.Error())
		return
	}
	content := container.NewVBox(
		widget.NewLabel("These settings are temporary. Closing the app, losing the helper, or lease expiry triggers rollback. If restoration fails, save your work and reboot to reset."),
		widget.NewSeparator(),
	)
	for _, row := range rows {
		content.Add(widget.NewLabel(formatReviewRow(row)))
	}
	application := fyne.CurrentApp()
	if application == nil || application.Driver() == nil || len(application.Driver().AllWindows()) == 0 {
		return
	}
	dialog.NewCustomConfirm("Review temporary tuning", "Authenticate & apply", "Cancel", content, func(confirm bool) {
		if confirm {
			go func() { _ = page.viewModel.Apply(context.Background()) }()
		} else {
			page.viewModel.CancelReview()
		}
	}, application.Driver().AllWindows()[0]).Show()
}

func (page *Tune) showRebootGuidance() {
	application := fyne.CurrentApp()
	if application == nil || application.Driver() == nil || len(application.Driver().AllWindows()) == 0 {
		return
	}
	dialog.ShowInformation("Reboot to reset", "Save your work, then restart Linux using the system menu. If the computer has hard-locked, a power cycle is required. This app does not initiate a reboot.", application.Driver().AllWindows()[0])
}

func formatReviewRow(row viewmodel.ReviewRow) string {
	suffix := ""
	if row.Adjusted {
		suffix = "  · adjusted to a supported step"
	}
	return fmt.Sprintf("%s    Stock %s    Requested %s    Expected %s%s", row.Label, valueText(row.Stock), valueText(row.Requested), valueText(row.Normalized), suffix)
}

func (page *Tune) renderTelemetry(state viewmodel.MonitorState) {
	values := tuneTelemetrySnapshot(state, page.metrics)
	page.liveCards["temperature"].SetSample(values.temperature)
	page.liveCards["power"].SetSample(values.power)
	page.liveCards["frequency"].SetSample(values.frequency)
}

func sectionCard(title, subtitle string, content fyne.CanvasObject) fyne.CanvasObject {
	return widget.NewCard(title, subtitle, content)
}

func phaseLabel(phase, lastError string) string {
	if lastError != "" {
		return lastError
	}
	switch phase {
	case viewmodel.PhaseDiscovering:
		return "Discovering controls…"
	case viewmodel.PhaseStaged:
		return "Changes staged · hardware unchanged"
	case viewmodel.PhaseReviewing:
		return "Review required"
	case viewmodel.PhaseAuthorizing:
		return "Waiting for authorization…"
	case viewmodel.PhaseApplying:
		return "Applying and verifying…"
	case viewmodel.PhaseActive:
		return "Temporary tuning session active"
	case viewmodel.PhaseRollingBack:
		return "Restoring stock settings…"
	case viewmodel.PhaseRollbackIncomplete:
		return "Rollback incomplete · attention required"
	default:
		return "Hardware unchanged · adjust controls to begin"
	}
}

func valueText(value tuning.Value) string {
	switch value.Kind {
	case tuning.ValueNumeric:
		return fmt.Sprintf("%.2f", value.Number)
	case tuning.ValueChoice:
		return value.Choice
	case tuning.ValueVector:
		return fmt.Sprint(value.Vector)
	default:
		return "—"
	}
}

func classifyTuneTelemetry(catalog telemetry.Catalog) tuneTelemetryMetrics {
	var metrics tuneTelemetryMetrics
	for _, descriptor := range catalog.Metrics {
		id := strings.ToLower(string(descriptor.ID))
		label := strings.ToLower(descriptor.Label)
		switch {
		case metrics.temperature == "" && descriptor.Unit == "°C" && (strings.Contains(id, "package") || strings.Contains(label, "package")):
			metrics.temperature = descriptor.ID
		case metrics.power == "" && descriptor.Unit == "W" && (strings.Contains(id, "package") || strings.Contains(label, "package")):
			metrics.power = descriptor.ID
		case descriptor.Unit == "MHz" && strings.Contains(id, "cpu"):
			metrics.frequencies = append(metrics.frequencies, descriptor.ID)
		}
	}
	return metrics
}

func tuneTelemetryIDs(metrics tuneTelemetryMetrics) []telemetry.MetricID {
	result := make([]telemetry.MetricID, 0, 2+len(metrics.frequencies))
	if metrics.temperature != "" {
		result = append(result, metrics.temperature)
	}
	if metrics.power != "" {
		result = append(result, metrics.power)
	}
	return append(result, metrics.frequencies...)
}

func tuneTelemetrySnapshot(state viewmodel.MonitorState, metrics tuneTelemetryMetrics) tuneTelemetryValues {
	values := tuneTelemetryValues{
		temperature: telemetry.Sample{MetricID: metrics.temperature, Quality: telemetry.QualityUnavailable},
		power:       telemetry.Sample{MetricID: metrics.power, Quality: telemetry.QualityUnavailable},
		frequency:   telemetry.Sample{MetricID: "cpu.maximum.frequency", Quality: telemetry.QualityUnavailable},
	}
	if sample, exists := state.Current[metrics.temperature]; exists {
		values.temperature = sample
	}
	if sample, exists := state.Current[metrics.power]; exists {
		values.power = sample
	}
	for _, metricID := range metrics.frequencies {
		sample, exists := state.Current[metricID]
		if !exists || sample.Quality == telemetry.QualityUnavailable {
			continue
		}
		if values.frequency.Quality == telemetry.QualityUnavailable || sample.Value > values.frequency.Value {
			values.frequency = sample
		}
	}
	return values
}
