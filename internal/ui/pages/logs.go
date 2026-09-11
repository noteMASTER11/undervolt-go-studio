package pages

import (
	"fmt"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/noteMASTER11/undervolt-go-studio/internal/events"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
	"github.com/noteMASTER11/undervolt-go-studio/internal/ui/components"
)

type Logs struct {
	store *events.Store

	mu     sync.Mutex
	active bool
	cancel func()
	events []tuning.Event

	rows       *fyne.Container
	detail     *widget.Label
	dispatcher *components.LatestDispatcher[[]tuning.Event]
	root       fyne.CanvasObject
}

func NewLogs(store *events.Store) *Logs {
	page := &Logs{store: store, rows: container.NewVBox()}
	page.detail = widget.NewLabel("Select an event to see technical details")
	page.detail.Wrapping = fyne.TextWrapWord
	page.dispatcher = components.NewLatestDispatcher(func(callback func()) { fyne.Do(callback) }, page.render)
	header := container.NewVBox(
		widget.NewLabelWithStyle("Logs", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("Tuning actions, safety checks, and automatic restoration"),
		widget.NewSeparator(),
	)
	page.root = container.NewPadded(container.NewBorder(header, widget.NewCard("Technical detail", "", page.detail), nil, nil, container.NewVScroll(page.rows)))
	page.render(store.Snapshot())
	return page
}

func (page *Logs) ID() string                { return "logs" }
func (page *Logs) Object() fyne.CanvasObject { return page.root }

func (page *Logs) Activate() {
	page.mu.Lock()
	if page.active {
		page.mu.Unlock()
		return
	}
	page.active = true
	updates, cancel := page.store.Subscribe()
	page.cancel = cancel
	page.mu.Unlock()
	page.dispatcher.Submit(page.store.Snapshot())
	go func() {
		for range updates {
			page.dispatcher.Submit(page.store.Snapshot())
		}
	}()
}

func (page *Logs) Deactivate() {
	page.mu.Lock()
	if !page.active {
		page.mu.Unlock()
		return
	}
	page.active = false
	cancel := page.cancel
	page.cancel = nil
	page.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (page *Logs) render(items []tuning.Event) {
	page.mu.Lock()
	page.events = append([]tuning.Event(nil), items...)
	page.mu.Unlock()
	page.rows.RemoveAll()
	if len(items) == 0 {
		page.rows.Add(container.NewCenter(widget.NewLabel("No Studio events yet")))
		page.rows.Refresh()
		return
	}
	for index := len(items) - 1; index >= 0; index-- {
		event := items[index]
		caption := fmt.Sprintf("%s   %-7s   %s", event.Time.Format("15:04:05"), event.Kind, event.Message)
		button := widget.NewButton(caption, func() {
			detail := event.Detail
			if detail == "" {
				detail = "No additional technical detail"
			}
			page.detail.SetText(detail)
		})
		button.Alignment = widget.ButtonAlignLeading
		page.rows.Add(button)
	}
	page.rows.Refresh()
}
