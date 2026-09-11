package components

import (
	"fmt"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
)

type PendingRail struct {
	mu            sync.RWMutex
	changes       []tuning.Change
	reviewAllowed bool
	rows          *fyne.Container
	review        *widget.Button
	revert        *widget.Button
	object        fyne.CanvasObject
}

func NewPendingRail(onReset, onReview, onRevert func()) *PendingRail {
	rail := &PendingRail{rows: container.NewVBox()}
	reset := widget.NewButton("Reset", onReset)
	rail.review = widget.NewButton("Review & apply", onReview)
	rail.revert = widget.NewButton("Revert active session", onRevert)
	rail.review.Importance = widget.HighImportance
	rail.review.Disable()
	rail.revert.Disable()
	rail.object = widget.NewCard("Pending changes", "Nothing is written until confirmation", container.NewVBox(
		rail.rows,
		widget.NewSeparator(),
		container.NewGridWithColumns(2, reset, rail.review),
		rail.revert,
	))
	rail.SetChanges(nil)
	return rail
}

func (rail *PendingRail) Object() fyne.CanvasObject { return rail.object }

func (rail *PendingRail) SetChanges(changes []tuning.Change, sets ...tuning.CapabilitySet) {
	rail.mu.Lock()
	rail.changes = append([]tuning.Change(nil), changes...)
	rail.mu.Unlock()
	capabilities := make(map[tuning.ControlID]tuning.Capability)
	if len(sets) > 0 {
		for _, capability := range sets[0].Capabilities {
			capabilities[capability.ID] = capability
		}
	}
	rail.rows.RemoveAll()
	if len(changes) == 0 {
		rail.rows.Add(widget.NewLabel("Adjust a control to stage it here."))
	} else {
		for _, change := range changes {
			capability, exists := capabilities[change.ID]
			if exists {
				rail.rows.Add(widget.NewLabel(fmt.Sprintf("%s   %s  →  %s", shortControlName(change.ID), formatTuningValue(capability.Current, capability.Unit), formatTuningValue(change.Requested, capability.Unit))))
			} else {
				rail.rows.Add(widget.NewLabel(fmt.Sprintf("%s  →  %s", shortControlName(change.ID), formatTuningValue(change.Requested, ""))))
			}
		}
	}
	rail.updateReviewButton()
	rail.rows.Refresh()
}

func (rail *PendingRail) SetReviewEnabled(enabled bool) {
	rail.mu.Lock()
	rail.reviewAllowed = enabled
	rail.mu.Unlock()
	rail.updateReviewButton()
}

func (rail *PendingRail) SetSessionActive(active bool) {
	if active {
		rail.revert.Enable()
	} else {
		rail.revert.Disable()
	}
}

func (rail *PendingRail) Count() int {
	rail.mu.RLock()
	defer rail.mu.RUnlock()
	return len(rail.changes)
}

func (rail *PendingRail) updateReviewButton() {
	rail.mu.RLock()
	enabled := rail.reviewAllowed && len(rail.changes) > 0
	rail.mu.RUnlock()
	if enabled {
		rail.review.Enable()
	} else {
		rail.review.Disable()
	}
}

func shortControlName(id tuning.ControlID) string {
	switch id {
	case tuning.ControlPL1:
		return "PL1"
	case tuning.ControlPL2:
		return "PL2"
	case tuning.ControlTau:
		return "Turbo window"
	case tuning.ControlEPP:
		return "Energy policy"
	case tuning.ControlThermalLimit:
		return "Thermal limit"
	case tuning.ControlRatioPCore:
		return "P-core ratios"
	case tuning.ControlRatioECore:
		return "E-core ratios"
	case tuning.ControlVoltageCore:
		return "Core voltage"
	case tuning.ControlVoltageCache:
		return "Cache voltage"
	default:
		return string(id)
	}
}
