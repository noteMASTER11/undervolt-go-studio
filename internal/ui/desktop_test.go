package ui

import (
	"errors"
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
	t.Cleanup(func() { _ = desktop.Close() })

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
		beginWindowClose(func() error { <-release; return nil }, func(callback func()) { callback() }, func() { close(finished) }, func(error) { t.Error("unexpected close failure") })
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

func TestWindowCloseFailureRemainsVisibleAndKeepsWindowOpen(t *testing.T) {
	shown := make(chan error, 1)
	finished := make(chan struct{}, 1)
	want := errors.New("Stock restoration incomplete")
	beginWindowClose(func() error { return want }, func(callback func()) { callback() }, func() { finished <- struct{}{} }, func(err error) { shown <- err })
	select {
	case err := <-shown:
		if !errors.Is(err, want) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("rollback failure discarded")
	}
	select {
	case <-finished:
		t.Fatal("window closed despite rollback failure")
	default:
	}
}

func TestDesktopCloseIsIdempotent(t *testing.T) {
	desktop := NewDesktop(product.Current("test"))
	desktop.Close()
	desktop.Close()
}
