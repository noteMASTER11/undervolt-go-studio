package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/noteMASTER11/undervolt-go-studio/internal/product"
	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
)

func TestShellProvidesEveryApprovedDestination(t *testing.T) {
	application := test.NewApp()
	defer application.Quit()
	shell := NewShell(product.Current("test"), telemetry.NewScheduler(nil, telemetry.SchedulerOptions{}))

	want := []string{"overview", "monitor", "tune", "stress", "profiles", "reports", "hardware", "logs"}
	for _, id := range want {
		if err := shell.Select(id); err != nil {
			t.Fatalf("select %q: %v", id, err)
		}
	}
	if shell.Object() == nil {
		t.Fatal("shell has no canvas object")
	}
}

func TestShellKeepsTuningActionsDisabledInReadOnlyMilestone(t *testing.T) {
	application := test.NewApp()
	defer application.Quit()
	shell := NewShell(product.Current("test"), telemetry.NewScheduler(nil, telemetry.SchedulerOptions{}))
	if !shell.applyButton.Disabled() || !shell.revertButton.Disabled() {
		t.Fatal("read-only shell exposed tuning actions")
	}
}
