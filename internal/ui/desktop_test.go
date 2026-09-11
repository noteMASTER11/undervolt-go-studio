package ui

import (
	"testing"
	"time"

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

func TestBeginWindowCloseKeepsUIHandlerNonBlocking(t *testing.T) {
	release := make(chan struct{})
	finished := make(chan struct{})
	returned := make(chan struct{})
	go func() {
		beginWindowClose(func() { <-release }, func(callback func()) { callback() }, func() { close(finished) })
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("window close handler blocked on rollback")
	}
	select {
	case <-finished:
		t.Fatal("window closed before rollback work finished")
	default:
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("window did not close after rollback work")
	}
}

func TestDesktopCloseIsIdempotent(t *testing.T) {
	desktop := NewDesktop(product.Current("test"))
	desktop.Close()
	desktop.Close()
}
