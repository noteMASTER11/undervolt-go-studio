package powercap

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/testkit"
)

func TestProbeCombinesMSRAndMMIOPackageConstraints(t *testing.T) {
	store := powercapStore(map[string]string{
		"intel-rapl:0/name":                                 "package-0\n",
		"intel-rapl:0/constraint_0_name":                    "long_term\n",
		"intel-rapl:0/constraint_0_power_limit_uw":          "44000000\n",
		"intel-rapl:0/constraint_0_max_power_uw":            "55000000\n",
		"intel-rapl-mmio:0/name":                            "package-0\n",
		"intel-rapl-mmio:0/constraint_0_name":               "long_term\n",
		"intel-rapl-mmio:0/constraint_0_power_limit_uw":     "44000000\n",
		"intel-rapl-mmio:0/constraint_0_max_power_uw":       "55000000\n",
		"intel-rapl-mmio:0/constraint_0_time_window_us":     "27983872\n",
		"intel-rapl-mmio:0/constraint_0_max_time_window_us": "55967744\n",
		"intel-rapl:0/constraint_0_time_window_us":          "27983872\n",
		"intel-rapl:0/constraint_0_max_time_window_us":      "55967744\n",
	})

	caps, err := New(store).Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pl1 := capabilityByID(t, caps, tuning.ControlPL1)
	if pl1.Current.Number != 44 || pl1.Range == nil || pl1.Range.Maximum != 55 {
		t.Fatalf("PL1 = %+v", pl1)
	}
	if pl1.State != tuning.StateSupported {
		t.Fatalf("PL1 state = %q", pl1.State)
	}
	if pl1.Range.Step != 1 {
		t.Fatalf("sysfs units advertised as target resolution: %+v", pl1.Range)
	}
	tau := capabilityByID(t, caps, tuning.ControlTau)
	if tau.State != tuning.StateReadOnly || tau.Range != nil {
		t.Fatalf("unproved time resolution advertised writable: %+v", tau)
	}
}

func TestGenerationChangesWhenHiddenPowercapSourceChanges(t *testing.T) {
	store := powercapStore(map[string]string{
		"intel-rapl:0/name": "package-0", "intel-rapl:0/constraint_0_name": "long_term",
		"intel-rapl:0/constraint_0_power_limit_uw": "44000000", "intel-rapl:0/constraint_0_max_power_uw": "55000000",
		"intel-rapl-mmio:0/name": "package-0", "intel-rapl-mmio:0/constraint_0_name": "long_term",
		"intel-rapl-mmio:0/constraint_0_power_limit_uw": "45000000", "intel-rapl-mmio:0/constraint_0_max_power_uw": "55000000",
	})
	d := tuning.NewDiscoverer("cpu", New(store))
	probe := func() tuning.CapabilitySet {
		var set tuning.CapabilitySet
		for r := range d.Discover(context.Background()) {
			set = r.Set
		}
		return set
	}
	before := probe()
	store.Files[root+"/intel-rapl-mmio:0/constraint_0_power_limit_uw"] = []byte("46000000")
	after := probe()
	if before.Generation == after.Generation {
		t.Fatal("hidden source drift kept reviewed generation")
	}
	if before.Capabilities[0].Current.Number != after.Capabilities[0].Current.Number {
		t.Fatal("fixture changed visible effective value")
	}
}

func TestProbeMapsShortTermByNameAndTreatsZeroMaximumAsUnknown(t *testing.T) {
	store := powercapStore(map[string]string{
		"intel-rapl:0/name":                        "package-0\n",
		"intel-rapl:0/constraint_7_name":           "short_term\n",
		"intel-rapl:0/constraint_7_power_limit_uw": "44000000\n",
		"intel-rapl:0/constraint_7_max_power_uw":   "0\n",
	})

	caps, err := New(store).Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pl2 := capabilityByID(t, caps, tuning.ControlPL2)
	if pl2.Current.Number != 44 {
		t.Fatalf("PL2 current = %+v", pl2.Current)
	}
	if pl2.Range != nil {
		t.Fatalf("zero maximum became range %+v", pl2.Range)
	}
	if pl2.State != tuning.StateReadOnly {
		t.Fatalf("PL2 state = %q", pl2.State)
	}
}

func TestProbeIgnoresEmptyMaximumInsteadOfParsingZero(t *testing.T) {
	store := powercapStore(map[string]string{
		"intel-rapl:0/name":                        "package-0\n",
		"intel-rapl:0/constraint_1_name":           "short_term\n",
		"intel-rapl:0/constraint_1_power_limit_uw": "44000000\n",
		"intel-rapl:0/constraint_1_max_power_uw":   "\n",
	})

	caps, err := New(store).Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := capabilityByID(t, caps, tuning.ControlPL2); got.Range != nil || got.Current.Number != 44 {
		t.Fatalf("PL2 = %+v", got)
	}
}

func TestPL1OperationWritesEveryParticipatingSourceAndRestoresEachValue(t *testing.T) {
	store := powercapStore(map[string]string{
		"intel-rapl:0/name":                             "package-0\n",
		"intel-rapl:0/constraint_0_name":                "long_term\n",
		"intel-rapl:0/constraint_0_power_limit_uw":      "44000000\n",
		"intel-rapl:0/constraint_0_max_power_uw":        "55000000\n",
		"intel-rapl-mmio:0/name":                        "package-0\n",
		"intel-rapl-mmio:0/constraint_0_name":           "long_term\n",
		"intel-rapl-mmio:0/constraint_0_power_limit_uw": "45000000\n",
		"intel-rapl-mmio:0/constraint_0_max_power_uw":   "55000000\n",
	})
	driver := New(store)
	if _, err := driver.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	op, err := driver.Prepare(context.Background(), tuning.Change{ID: tuning.ControlPL1, Requested: tuning.NumericValue(40)})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := op.Capture(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	effective, err := op.Apply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if effective.Number != 40 {
		t.Fatalf("effective = %+v", effective)
	}
	assertMicroValue(t, store, "intel-rapl:0/constraint_0_power_limit_uw", 40000000)
	assertMicroValue(t, store, "intel-rapl-mmio:0/constraint_0_power_limit_uw", 40000000)

	if _, err := op.Restore(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	assertMicroValue(t, store, "intel-rapl:0/constraint_0_power_limit_uw", 44000000)
	assertMicroValue(t, store, "intel-rapl-mmio:0/constraint_0_power_limit_uw", 45000000)
}

func TestOperationRollsBackEarlierSourceWhenLaterWriteFails(t *testing.T) {
	store := powercapStore(map[string]string{
		"intel-rapl:0/name":                             "package-0\n",
		"intel-rapl:0/constraint_0_name":                "long_term\n",
		"intel-rapl:0/constraint_0_power_limit_uw":      "44000000\n",
		"intel-rapl:0/constraint_0_max_power_uw":        "55000000\n",
		"intel-rapl-mmio:0/name":                        "package-0\n",
		"intel-rapl-mmio:0/constraint_0_name":           "long_term\n",
		"intel-rapl-mmio:0/constraint_0_power_limit_uw": "45000000\n",
		"intel-rapl-mmio:0/constraint_0_max_power_uw":   "55000000\n",
	})
	driver := New(store)
	if _, err := driver.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	op, err := driver.Prepare(context.Background(), tuning.Change{ID: tuning.ControlPL1, Requested: tuning.NumericValue(40)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := op.Capture(context.Background()); err != nil {
		t.Fatal(err)
	}
	store.Fail("write", rootPath("intel-rapl-mmio:0/constraint_0_power_limit_uw"), errors.New("firmware rejected write"))

	if _, err := op.Apply(context.Background()); err == nil {
		t.Fatal("apply succeeded despite second-source failure")
	}
	assertMicroValue(t, store, "intel-rapl:0/constraint_0_power_limit_uw", 44000000)
	assertMicroValue(t, store, "intel-rapl-mmio:0/constraint_0_power_limit_uw", 45000000)
}

func powercapStore(files map[string]string) *testkit.MemoryStore {
	prefixed := make(map[string]string, len(files))
	for name, value := range files {
		prefixed[rootPath(name)] = value
	}
	return testkit.NewMemoryStore(prefixed)
}

func rootPath(name string) string {
	return "sys/class/powercap/" + name
}

func capabilityByID(t *testing.T, capabilities []tuning.Capability, id tuning.ControlID) tuning.Capability {
	t.Helper()
	for _, capability := range capabilities {
		if capability.ID == id {
			return capability
		}
	}
	t.Fatalf("capability %s not found in %#v", id, capabilities)
	return tuning.Capability{}
}

func assertMicroValue(t *testing.T, store *testkit.MemoryStore, name string, want int64) {
	t.Helper()
	raw, err := store.Read(rootPath(name))
	if err != nil {
		t.Fatal(err)
	}
	var got int64
	if _, err := fmt.Sscan(string(raw), &got); err != nil {
		t.Fatal(err)
	}
	if math.Abs(float64(got-want)) > 0 {
		t.Fatalf("%s = %d, want %d", name, got, want)
	}
}
