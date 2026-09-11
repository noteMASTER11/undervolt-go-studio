package pages

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// Placeholder keeps future destinations lazy without implying functionality.
type Placeholder struct {
	id     string
	object fyne.CanvasObject
}

func NewPlaceholder(id, title, message string) *Placeholder {
	return &Placeholder{
		id: id,
		object: container.NewPadded(container.NewVBox(
			widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			widget.NewSeparator(),
			widget.NewLabel(message),
		)),
	}
}

func (p *Placeholder) ID() string                { return p.id }
func (p *Placeholder) Object() fyne.CanvasObject { return p.object }
func (p *Placeholder) Activate()                 {}
func (p *Placeholder) Deactivate()               {}
