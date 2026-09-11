package components

import (
	"fmt"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
)

type TuneControl struct {
	capability tuning.Capability
	enabled    bool
	status     string
	object     fyne.CanvasObject
}

func NewTuneControl(capability tuning.Capability, onStage func(tuning.ControlID, tuning.Value)) *TuneControl {
	control := &TuneControl{capability: capability}
	control.enabled = capability.State == tuning.StateSupported && onStage != nil
	control.status = capabilityStatus(capability)
	current := widget.NewLabelWithStyle(formatTuningValue(capability.Current, capability.Unit), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	status := widget.NewLabel(control.status)
	status.Wrapping = fyne.TextWrapWord
	editor := control.editor(onStage)
	content := container.NewVBox(container.NewBorder(nil, nil, nil, current, widget.NewLabel(capability.Scope)), editor, status)
	control.object = widget.NewCard(capability.Label, "", content)
	return control
}

func (control *TuneControl) Object() fyne.CanvasObject { return control.object }
func (control *TuneControl) Enabled() bool             { return control.enabled }
func (control *TuneControl) StatusText() string        { return control.status }

func (control *TuneControl) editor(onStage func(tuning.ControlID, tuning.Value)) fyne.CanvasObject {
	capability := control.capability
	if capability.Range != nil && capability.Current.Kind == tuning.ValueNumeric {
		slider := widget.NewSlider(capability.Range.Minimum, capability.Range.Maximum)
		slider.Step = capability.Range.Step
		slider.SetValue(capability.Current.Number)
		value := widget.NewLabel(formatTuningValue(capability.Current, capability.Unit))
		slider.OnChanged = func(number float64) {
			value.SetText(formatTuningValue(tuning.NumericValue(number), capability.Unit))
			if control.enabled {
				onStage(capability.ID, tuning.NumericValue(number))
			}
		}
		if !control.enabled {
			slider.Disable()
		}
		return container.NewBorder(nil, nil, nil, value, slider)
	}
	if capability.Unit == tuning.UnitChoice {
		selectWidget := widget.NewSelect(capability.Choices, nil)
		selectWidget.SetSelected(capability.Current.Choice)
		selectWidget.OnChanged = func(choice string) {
			if control.enabled {
				onStage(capability.ID, tuning.ChoiceValue(choice))
			}
		}
		if !control.enabled {
			selectWidget.Disable()
		}
		return selectWidget
	}
	if capability.Unit == tuning.UnitRatio && len(capability.Current.Vector) > 0 {
		values := append([]float64(nil), capability.Current.Vector...)
		row := container.NewHBox()
		for index, ratio := range values {
			entry := widget.NewEntry()
			entry.SetText(strconv.Itoa(int(ratio)))
			entry.SetPlaceHolder(fmt.Sprintf("%dC", index+1))
			entry.OnChanged = func(text string) {
				parsed, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
				if err == nil && control.enabled {
					values[index] = parsed
					onStage(capability.ID, tuning.VectorValue(values))
				}
			}
			if !control.enabled {
				entry.Disable()
			}
			row.Add(container.NewGridWrap(fyne.NewSize(52, 38), entry))
		}
		return row
	}
	return widget.NewLabel("No adjustable range was reported")
}

func capabilityStatus(capability tuning.Capability) string {
	if capability.Reason != "" {
		return capability.Reason
	}
	switch capability.State {
	case tuning.StateSupported:
		if capability.Experimental {
			return "Available · experimental · session end triggers rollback"
		}
		return "Available · temporary · session end triggers rollback"
	case tuning.StateReadOnly:
		return "Read-only on this system"
	case tuning.StateFirmwareLocked:
		return "BIOS or firmware protection blocks this control"
	case tuning.StateKernelBlocked:
		return "The active kernel interface blocks this control"
	case tuning.StateRequiresProbe:
		return "Authentication is required to check this control"
	case tuning.StateUnknownModel:
		return "This processor model is not in the verified allow-list"
	default:
		return "Unavailable on this system"
	}
}

func formatTuningValue(value tuning.Value, unit tuning.Unit) string {
	switch value.Kind {
	case tuning.ValueNumeric:
		return fmt.Sprintf("%.1f %s", value.Number, unit)
	case tuning.ValueChoice:
		return value.Choice
	case tuning.ValueVector:
		parts := make([]string, len(value.Vector))
		for index, item := range value.Vector {
			parts[index] = strconv.Itoa(int(item))
		}
		return strings.Join(parts, " / ")
	default:
		return "—"
	}
}
