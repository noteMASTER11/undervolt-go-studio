package pages

import (
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"github.com/noteMASTER11/undervolt-go-studio/internal/product"
	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
)

func TestHardwareRowsExposeProviderHealth(t *testing.T) {
	in := []telemetry.ProviderDiagnostics{{
		ProviderID: "linux.hwmon", State: "backoff", LastError: "permission denied",
		ActiveMetrics: 3, ActiveConsumers: 1, DroppedFrames: 2,
	}}
	rows := diagnosticsRows(in)
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	if rows[0].Provider != "linux.hwmon" || rows[0].State != "backoff" || rows[0].Dropped != 2 {
		t.Fatalf("row = %+v", rows[0])
	}
}

type diagnosticsSource struct{}

func (diagnosticsSource) Diagnostics() []telemetry.ProviderDiagnostics { return nil }

func TestHardwareRefreshLifecycleIsIdempotent(t *testing.T) {
	application := test.NewApp()
	defer application.Quit()
	page := NewHardware(product.Current("test"), diagnosticsSource{}, telemetry.Catalog{})
	page.Activate()
	page.Activate()
	page.Deactivate()
	page.Deactivate()
	page.mu.Lock()
	defer page.mu.Unlock()
	if page.active || page.stop != nil || page.done != nil {
		t.Fatal("hardware refresh loop remained active")
	}
}

func TestDiagnosticsJSONContainsInventoryWithoutHostPaths(t *testing.T) {
	payload, err := diagnosticsJSON(product.Current("test"), telemetry.Catalog{
		Metrics: []telemetry.Descriptor{{ID: "cpu.utilization", ProviderID: "linux.procstat", Unit: "%"}},
	}, []telemetry.ProviderDiagnostics{{ProviderID: "linux.procstat", LastSuccess: time.Unix(1, 0)}})
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	if !strings.Contains(text, "cpu.utilization") || !strings.Contains(text, "linux.procstat") {
		t.Fatalf("missing inventory: %s", text)
	}
	if strings.Contains(text, "/home/") || strings.Contains(text, "HOME=") {
		t.Fatalf("diagnostics leaked host data: %s", text)
	}
}
