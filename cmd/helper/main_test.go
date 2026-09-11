package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/noteMASTER11/undervolt-go-studio/internal/privilege/protocol"
	"github.com/noteMASTER11/undervolt-go-studio/internal/privilege/session"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/intel/powercap"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/testkit"
)

func TestHelperRestoresHydratedKernelSnapshotBeforePublishingStock(t *testing.T) {
	const base = "sys/class/powercap/intel-rapl:0/"
	store := testkit.NewMemoryStore(map[string]string{base + "name": "package-0", base + "constraint_0_name": "long_term", base + "constraint_0_power_limit_uw": "40000000", base + "constraint_0_max_power_uw": "55000000"})
	driver := powercap.New(store)
	recovery := tuning.FileRecoveryStore{Directory: t.TempDir()}
	if err := recovery.Save(tuning.RecoveryRecord{Protocol: 1, MachineID: "cpu", BootID: "boot", Entries: []tuning.RecoveryEntry{{DriverID: driver.ID(), ControlID: tuning.ControlPL1, Applied: true, Snapshot: json.RawMessage(`[{"path":"` + base + `constraint_0_power_limit_uw","value":44000000}]`)}}}); err != nil {
		t.Fatal(err)
	}
	engine := tuning.NewEngine(tuning.CapabilitySet{MachineID: "cpu"}, recovery, driver)
	engine.BootID = "boot"
	backend := &helperBackend{discoverer: tuning.NewDiscoverer("cpu", driver), engine: engine}
	var input, output bytes.Buffer
	writer := protocol.NewWriter(&input)
	for _, message := range []protocol.Message{{Version: 1, RequestID: "h", Type: protocol.TypeHello, Payload: json.RawMessage(`{"client":"test"}`)}, {Version: 1, RequestID: "p", Type: protocol.TypeProbe}} {
		if err := writer.Write(message); err != nil {
			t.Fatal(err)
		}
	}
	if err := session.NewServer(backend).Run(context.Background(), protocol.NewReader(&input), protocol.NewWriter(&output)); err != nil {
		t.Fatal(err)
	}
	if string(store.Files[base+"constraint_0_power_limit_uw"]) != "44000000" {
		t.Fatal("kernel source was not restored")
	}
	if _, err := recovery.Load(); !os.IsNotExist(err) {
		t.Fatalf("verified recovery record remained: %v", err)
	}
	if engine.Capabilities.Capabilities[0].Current.Number != 44 {
		t.Fatalf("published stale pre-recovery value: %+v", engine.Capabilities)
	}
}
