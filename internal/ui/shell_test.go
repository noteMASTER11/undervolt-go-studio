package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/noteMASTER11/undervolt-go-studio/internal/product"
	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
	"github.com/noteMASTER11/undervolt-go-studio/internal/ui/pages"
)

func TestShellProvidesEveryApprovedDestination(t *testing.T) {
	application := test.NewApp()
	defer application.Quit()
	shell := NewShell(product.Current("test"), telemetry.NewScheduler(nil, telemetry.SchedulerOptions{}))
	defer shell.Deactivate()

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

func TestShellUsesConcreteLivePagesAndKeepsMonitorLazy(t *testing.T) {
	application := test.NewApp()
	defer application.Quit()
	shell := NewShell(product.Current("test"), telemetry.NewScheduler(nil, telemetry.SchedulerOptions{}))
	defer shell.Deactivate()
	if _, ok := shell.navigator.pages["overview"].(*pages.Overview); !ok {
		t.Fatalf("overview type = %T", shell.navigator.pages["overview"])
	}
	if _, exists := shell.navigator.pages["monitor"]; exists {
		t.Fatal("monitor was created before selection")
	}
	shell.SetCatalog(telemetry.Catalog{Metrics: []telemetry.Descriptor{{ID: "cpu.utilization", Label: "CPU Utilization", Unit: "%"}}})
	if err := shell.Select("monitor"); err != nil {
		t.Fatal(err)
	}
	if _, ok := shell.navigator.pages["monitor"].(*pages.Monitor); !ok {
		t.Fatalf("monitor type = %T", shell.navigator.pages["monitor"])
	}
	if err := shell.Select("hardware"); err != nil {
		t.Fatal(err)
	}
	if _, ok := shell.navigator.pages["hardware"].(*pages.Hardware); !ok {
		t.Fatalf("hardware type = %T", shell.navigator.pages["hardware"])
	}
}

func TestShellKeepsTuningActionsDisabledInReadOnlyMilestone(t *testing.T) {
	application := test.NewApp()
	defer application.Quit()
	shell := NewShell(product.Current("test"), telemetry.NewScheduler(nil, telemetry.SchedulerOptions{}))
	defer shell.Deactivate()
	if !shell.applyButton.Disabled() || !shell.revertButton.Disabled() {
		t.Fatal("read-only shell exposed tuning actions")
	}
}
