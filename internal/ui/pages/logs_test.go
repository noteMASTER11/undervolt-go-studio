package pages

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"github.com/noteMASTER11/undervolt-go-studio/internal/events"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
)

func TestLogsPageActivationLifecycleIsIdempotent(t *testing.T) {
	application := test.NewApp()
	defer application.Quit()
	store := events.NewStore(10)
	page := NewLogs(store)
	page.Activate()
	page.Activate()
	store.Append(tuning.Event{Time: time.Now(), Kind: "info", Message: "PL1 restored to 44 W"})
	time.Sleep(20 * time.Millisecond)
	page.Deactivate()
	page.Deactivate()
	page.mu.Lock()
	defer page.mu.Unlock()
	if page.active || page.cancel != nil {
		t.Fatal("logs subscription remained active")
	}
}
