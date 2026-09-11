package pages

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

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

func TestHardwareSummaryParsersReturnHumanReadableValues(t *testing.T) {
	osValues := parseKeyValues("NAME=CachyOS\nPRETTY_NAME=\"CachyOS Linux\"\n")
	if osValues["PRETTY_NAME"] != "CachyOS Linux" {
		t.Fatalf("PRETTY_NAME = %q", osValues["PRETTY_NAME"])
	}
	if total := parseKilobytes("32768000 kB"); total != 32768000*1024 {
		t.Fatalf("memory bytes = %d", total)
	}
	if got := humanBytes(2 * 1024 * 1024 * 1024 * 1024); got != "2.0 TiB" {
		t.Fatalf("drive size = %q", got)
	}
}

func TestParseLSPCIKeepsOnlyGraphicsAdapters(t *testing.T) {
	input := `00:02.0 "VGA compatible controller" "Intel Corporation" "Arrow Lake-S [Intel Graphics]" -r02
02:00.0 "3D controller" "NVIDIA Corporation" "GeForce RTX 5080 Laptop GPU" -r01
80:14.5 "Non-VGA unclassified device" "Intel Corporation" "Device 7f2f" -r10
80:14.3 "Network controller" "Intel Corporation" "Wi-Fi Adapter" -r00`
	want := []string{
		"Intel Arrow Lake-S [Intel Graphics]",
		"NVIDIA GeForce RTX 5080 Laptop GPU",
	}
	if got := parseLSPCI(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("graphics = %#v, want %#v", got, want)
	}
}

func TestHardwareRowRendersLastSuccess(t *testing.T) {
	rows := diagnosticsRows([]telemetry.ProviderDiagnostics{{ProviderID: "linux.hwmon", LastSuccess: time.Unix(1, 0).UTC()}})
	if got := formatDiagnosticsRow(rows[0]); !strings.Contains(got, "1970-01-01T00:00:01Z") {
		t.Fatalf("rendered row omits last success: %q", got)
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

func TestHardwareSnapshotSummaryRendersWithoutHostDiscovery(t *testing.T) {
	application := test.NewApp()
	defer application.Quit()

	page := NewHardwareWithSummary(product.Current("snapshot"), diagnosticsSource{}, telemetry.Catalog{}, HardwareSummary{
		Machine:       "Studio validation workstation",
		OS:            "Linux",
		Kernel:        "Linux snapshot",
		CPU:           "Intel Core Ultra 9 275HX",
		CPUDetails:    []string{"24 cores · 24 logical processors"},
		Graphics:      []string{"Intel Arc Graphics"},
		Memory:        "32 GiB",
		MemoryDetails: []string{"12 GiB used · 20 GiB available"},
		Storage:       []string{"NVMe · 1.0 TiB"},
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := page.WaitForSummary(ctx); err != nil {
		t.Fatalf("wait for injected hardware summary: %v", err)
	}
	if got := hardwareSummaryText(page); !strings.Contains(got, "Intel Core Ultra 9 275HX") || !strings.Contains(got, "Studio validation workstation") {
		t.Fatalf("rendered summary = %q", got)
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

func hardwareSummaryText(page *Hardware) string {
	if len(page.summaryHost.Objects) != 1 {
		return ""
	}
	scroll, ok := page.summaryHost.Objects[0].(*container.Scroll)
	if !ok {
		return ""
	}
	var text []string
	var visit func(fyne.CanvasObject)
	visit = func(object fyne.CanvasObject) {
		switch item := object.(type) {
		case *widget.Label:
			text = append(text, item.Text)
		case *widget.Card:
			text = append(text, item.Title, item.Subtitle)
			if item.Content != nil {
				visit(item.Content)
			}
		case *fyne.Container:
			for _, child := range item.Objects {
				visit(child)
			}
		}
	}
	visit(scroll.Content)
	return strings.Join(text, "\n")
}
