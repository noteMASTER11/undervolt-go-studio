package ui

import (
	"fmt"

	"fyne.io/fyne/v2"
)

// Page is a lazily constructed navigation destination.
type Page interface {
	ID() string
	Object() fyne.CanvasObject
	Activate()
	Deactivate()
}

// PageFactory describes a destination without constructing it.
type PageFactory struct {
	ID     string
	Label  string
	Icon   fyne.Resource
	Create func() Page
}

// LazyNavigator owns page construction and activation lifecycle.
type LazyNavigator struct {
	factories []PageFactory
	byID      map[string]PageFactory
	pages     map[string]Page
	selected  Page
}

func NewLazyNavigator(factories []PageFactory) *LazyNavigator {
	byID := make(map[string]PageFactory, len(factories))
	for _, factory := range factories {
		byID[factory.ID] = factory
	}
	return &LazyNavigator{
		factories: append([]PageFactory(nil), factories...),
		byID:      byID,
		pages:     make(map[string]Page),
	}
}

func (n *LazyNavigator) Select(id string) (Page, error) {
	factory, exists := n.byID[id]
	if !exists {
		return nil, fmt.Errorf("unknown page %q", id)
	}
	page := n.pages[id]
	if page == nil {
		if factory.Create == nil {
			return nil, fmt.Errorf("page %q has no factory", id)
		}
		page = factory.Create()
		n.pages[id] = page
	}
	if n.selected == page {
		return page, nil
	}
	if n.selected != nil {
		n.selected.Deactivate()
	}
	n.selected = page
	n.selected.Activate()
	return page, nil
}

func (n *LazyNavigator) Factories() []PageFactory {
	return append([]PageFactory(nil), n.factories...)
}

func (n *LazyNavigator) Deactivate() {
	if n.selected != nil {
		n.selected.Deactivate()
		n.selected = nil
	}
}
