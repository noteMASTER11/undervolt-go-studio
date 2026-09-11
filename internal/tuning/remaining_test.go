package tuning_test

import (
	"context"
	"errors"
	"testing"

	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/intel/powercap"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/testkit"
)

func TestRollbackNeverReportsPartialPowercapProbeAsVerifiedRemaining(t *testing.T) {
	files := make(map[string]string)
	for _, zone := range []string{"intel-rapl:0", "intel-rapl-mmio:0"} {
		base := "sys/class/powercap/" + zone + "/"
		files[base+"name"] = "package-0"
		files[base+"constraint_0_name"] = "long_term"
		files[base+"constraint_0_power_limit_uw"] = "44000000"
		files[base+"constraint_0_max_power_uw"] = "55000000"
	}
	store := testkit.NewMemoryStore(files)
	driver := powercap.New(store)
	var caps tuning.CapabilitySet
	for result := range tuning.NewDiscoverer("cpu", driver).Discover(context.Background()) {
		caps = result.Set
	}
	engine := tuning.NewEngine(caps, tuning.FileRecoveryStore{Directory: t.TempDir()}, driver)
	active, _, err := engine.Apply(context.Background(), tuning.ChangeSet{Generation: caps.Generation, MachineID: "cpu", Changes: []tuning.Change{{ID: tuning.ControlPL1, Requested: tuning.NumericValue(40)}}})
	if err != nil {
		t.Fatal(err)
	}
	store.Fail("read", "sys/class/powercap/intel-rapl-mmio:0/constraint_0_power_limit_uw", errors.New("read unavailable"))
	err = active.Rollback(context.Background())
	var incomplete *tuning.RollbackError
	if !errors.As(err, &incomplete) {
		t.Fatalf("missing rollback outcome: %v", err)
	}
	if _, present := incomplete.Remaining[tuning.ControlPL1]; present {
		t.Fatal("partial source observation presented as fully verified remaining value")
	}
	if len(incomplete.Unverified) != 1 || incomplete.Unverified[0] != tuning.ControlPL1 {
		t.Fatalf("unreadable source not identified: %+v", incomplete)
	}
}
