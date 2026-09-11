package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

type fakePage struct {
	id          string
	activated   int
	deactivated int
	object      fyne.CanvasObject
}

func (p *fakePage) ID() string                { return p.id }
func (p *fakePage) Object() fyne.CanvasObject { return p.object }
func (p *fakePage) Activate()                 { p.activated++ }
func (p *fakePage) Deactivate()               { p.deactivated++ }

func TestLazyNavigatorCreatesPageOnce(t *testing.T) {
	application := test.NewApp()
	defer application.Quit()
	created := 0
	navigator := NewLazyNavigator([]PageFactory{{
		ID:    "monitor",
		Label: "Monitor",
		Create: func() Page {
			created++
			return &fakePage{id: "monitor", object: widget.NewLabel("Monitor")}
		},
	}})
	if created != 0 {
		t.Fatalf("pages created during registration = %d", created)
	}
	first, err := navigator.Select("monitor")
	if err != nil {
		t.Fatal(err)
	}
	second, err := navigator.Select("monitor")
	if err != nil {
		t.Fatal(err)
	}
	if created != 1 || first != second {
		t.Fatalf("created = %d, same page = %v", created, first == second)
	}
}

func TestLazyNavigatorDeactivatesPreviousPage(t *testing.T) {
	application := test.NewApp()
	defer application.Quit()
	first := &fakePage{id: "overview", object: widget.NewLabel("Overview")}
	second := &fakePage{id: "monitor", object: widget.NewLabel("Monitor")}
	navigator := NewLazyNavigator([]PageFactory{
		{ID: "overview", Label: "Overview", Create: func() Page { return first }},
		{ID: "monitor", Label: "Monitor", Create: func() Page { return second }},
	})
	if _, err := navigator.Select("overview"); err != nil {
		t.Fatal(err)
	}
	if _, err := navigator.Select("monitor"); err != nil {
		t.Fatal(err)
	}
	if first.deactivated != 1 || second.activated != 1 {
		t.Fatalf("overview deactivated = %d, monitor activated = %d", first.deactivated, second.activated)
	}
}

func TestLazyNavigatorRejectsUnknownPage(t *testing.T) {
	navigator := NewLazyNavigator(nil)
	if _, err := navigator.Select("missing"); err == nil {
		t.Fatal("unknown page was accepted")
	}
}
