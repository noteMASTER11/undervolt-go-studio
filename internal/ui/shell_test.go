package ui

import (
	"context"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/noteMASTER11/undervolt-go-studio/internal/events"
	"github.com/noteMASTER11/undervolt-go-studio/internal/product"
	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
	"github.com/noteMASTER11/undervolt-go-studio/internal/ui/pages"
)

func TestShellProvidesEveryApprovedDestination(t *testing.T) {
	application := test.NewApp()
	defer application.Quit()
	shell := newTestShell()
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
	shell := newTestShell()
	defer shell.Deactivate()
	if _, ok := shell.navigator.pages["overview"].(*pages.Overview); !ok {
		t.Fatalf("overview type = %T", shell.navigator.pages["overview"])
	}
	if _, exists := shell.navigator.pages["monitor"]; exists {
		t.Fatal("monitor was created before selection")
	}
	if _, exists := shell.navigator.pages["tune"]; exists {
		t.Fatal("tune was created before selection")
	}
	if err := shell.Select("tune"); err != nil {
		t.Fatal(err)
	}
	if _, ok := shell.navigator.pages["tune"].(*pages.Tune); !ok {
		t.Fatalf("tune type = %T", shell.navigator.pages["tune"])
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
	if shell.hardware == nil {
		t.Fatal("selected Hardware page is not available to readiness waits")
	}
}

func TestShellConstructsTuneLazily(t *testing.T) {
	application := test.NewApp()
	defer application.Quit()
	shell := newTestShell()
	defer shell.Deactivate()
	if _, exists := shell.navigator.pages["tune"]; exists {
		t.Fatal("tune was constructed eagerly")
	}
	if err := shell.Select("tune"); err != nil {
		t.Fatal(err)
	}
	if _, ok := shell.navigator.pages["tune"].(*pages.Tune); !ok {
		t.Fatalf("tune type = %T", shell.navigator.pages["tune"])
	}
}

func newTestShell() *Shell {
	return newShell(
		product.Current("test"), telemetry.NewScheduler(nil, telemetry.SchedulerOptions{}), events.NewStore(10),
		silentTuneService{}, func(callback func()) { callback() },
	)
}

type silentTuneService struct{}

func (silentTuneService) Discover(ctx context.Context) <-chan tuning.DiscoveryResult {
	results := make(chan tuning.DiscoveryResult)
	go func() {
		<-ctx.Done()
		close(results)
	}()
	return results
}

func (silentTuneService) Apply(context.Context, tuning.ChangeSet) (<-chan tuning.Event, error) {
	return nil, nil
}
func (silentTuneService) Revert(context.Context) error { return nil }
func (silentTuneService) Close(context.Context) error  { return nil }
