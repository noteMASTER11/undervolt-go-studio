package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/noteMASTER11/undervolt-go-studio/internal/product"
)

func TestDesktopBuildsWindowBeforeDiscovery(t *testing.T) {
	application := test.NewApp()
	t.Cleanup(application.Quit)

	desktop := NewDesktop(product.Current("v0.1.0-test"))
	window := desktop.Build(application)
	t.Cleanup(desktop.Close)

	if got, want := window.Title(), "Undervolt Go Studio"; got != want {
		t.Fatalf("window title = %q, want %q", got, want)
	}
	if window.Content() == nil {
		t.Fatal("window content is nil")
	}
	if desktop.started {
		t.Fatal("scheduler started while constructing the window")
	}
}

func TestDesktopCloseIsIdempotent(t *testing.T) {
	desktop := NewDesktop(product.Current("test"))
	desktop.Close()
	desktop.Close()
}
