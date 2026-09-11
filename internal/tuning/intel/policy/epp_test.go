package policy

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/testkit"
)

func TestEPPAppliesSameChoiceToEveryPolicyAndRestoresIndividually(t *testing.T) {
	store := eppStore(map[string]string{
		"policy0/energy_performance_available_preferences": "default performance balance_power\n",
		"policy0/energy_performance_preference":            "default\n",
		"policy8/energy_performance_available_preferences": "default performance balance_power\n",
		"policy8/energy_performance_preference":            "performance\n",
	})
	driver := New(store)
	if _, err := driver.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	op, err := driver.Prepare(context.Background(), tuning.Change{ID: tuning.ControlEPP, Requested: tuning.ChoiceValue("balance_power")})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := op.Capture(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := op.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertPreference(t, store, "policy0", "balance_power")
	assertPreference(t, store, "policy8", "balance_power")
	if _, err := op.Restore(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	assertPreference(t, store, "policy0", "default")
	assertPreference(t, store, "policy8", "performance")
	if _, err := store.Read(eppRoot + "/policy0/scaling_governor"); err == nil {
		t.Fatal("driver unexpectedly created or changed scaling_governor")
	}
}

func TestEPPChoicesAreIntersectionAcrossPolicies(t *testing.T) {
	store := eppStore(map[string]string{
		"policy0/energy_performance_available_preferences": "default performance balance_performance balance_power\n",
		"policy0/energy_performance_preference":            "default\n",
		"policy8/energy_performance_available_preferences": "performance balance_power power\n",
		"policy8/energy_performance_preference":            "performance\n",
	})

	caps, err := New(store).Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(caps) != 1 {
		t.Fatalf("capabilities = %#v", caps)
	}
	want := []string{"performance", "balance_power"}
	if !reflect.DeepEqual(caps[0].Choices, want) {
		t.Fatalf("choices = %#v, want %#v", caps[0].Choices, want)
	}
	if caps[0].Current.Choice != "mixed" {
		t.Fatalf("mixed current = %#v", caps[0].Current)
	}
}

func TestEPPIsKernelBlockedByIntelPstatePerformanceGovernor(t *testing.T) {
	store := eppStore(map[string]string{
		"policy0/energy_performance_available_preferences": "default performance balance_power power\n",
		"policy0/energy_performance_preference":            "default\n",
		"policy0/scaling_driver":                           "intel_pstate\n",
		"policy0/scaling_governor":                         "performance\n",
	})

	capabilities, err := New(store).Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(capabilities) != 1 {
		t.Fatalf("capabilities = %#v", capabilities)
	}
	capability := capabilities[0]
	if capability.State != tuning.StateKernelBlocked || capability.ReasonCode != "intel_pstate_performance_governor" || capability.Reason == "" {
		t.Fatalf("performance-governor capability = %+v", capability)
	}
}

func TestEPPFailsClosedWhenPolicyStateCannotBeRead(t *testing.T) {
	store := eppStore(map[string]string{
		"policy0/energy_performance_available_preferences": "default performance balance_power power\n",
		"policy0/energy_performance_preference":            "default\n",
		"policy0/scaling_driver":                           "intel_pstate\n",
		"policy0/scaling_governor":                         "powersave\n",
	})
	store.Fail("read", eppRoot+"/policy0/scaling_governor", errors.New("permission denied"))

	capabilities, err := New(store).Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(capabilities) != 1 {
		t.Fatalf("capabilities = %#v", capabilities)
	}
	capability := capabilities[0]
	if capability.State != tuning.StateKernelBlocked || capability.ReasonCode != "policy_state_unverified" || capability.Reason == "" {
		t.Fatalf("unverified-policy capability = %+v", capability)
	}
}

func TestEPPDetectsPerformanceGovernorBeforeSkippingUnreadablePreference(t *testing.T) {
	store := eppStore(map[string]string{
		"policy0/energy_performance_available_preferences": "default performance power\n",
		"policy0/energy_performance_preference":            "default\n",
		"policy0/scaling_driver":                           "intel_pstate\n",
		"policy0/scaling_governor":                         "powersave\n",
		"policy8/energy_performance_available_preferences": "default performance power\n",
		"policy8/energy_performance_preference":            "default\n",
		"policy8/scaling_driver":                           "intel_pstate\n",
		"policy8/scaling_governor":                         "performance\n",
	})
	store.Fail("read", eppRoot+"/policy8/energy_performance_preference", errors.New("permission denied"))

	capabilities, err := New(store).Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(capabilities) != 1 {
		t.Fatalf("capabilities = %#v", capabilities)
	}
	capability := capabilities[0]
	if capability.State != tuning.StateKernelBlocked || capability.ReasonCode != "intel_pstate_performance_governor" {
		t.Fatalf("mixed-policy capability = %+v", capability)
	}
}

func TestEPPFailsClosedWhenAnyPolicyPreferenceCannotBeRead(t *testing.T) {
	store := eppStore(map[string]string{
		"policy0/energy_performance_available_preferences": "default performance power\n",
		"policy0/energy_performance_preference":            "default\n",
		"policy0/scaling_driver":                           "intel_pstate\n",
		"policy0/scaling_governor":                         "powersave\n",
		"policy8/energy_performance_available_preferences": "default performance power\n",
		"policy8/energy_performance_preference":            "default\n",
		"policy8/scaling_driver":                           "intel_pstate\n",
		"policy8/scaling_governor":                         "powersave\n",
	})
	store.Fail("read", eppRoot+"/policy8/energy_performance_preference", errors.New("permission denied"))

	capabilities, err := New(store).Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(capabilities) != 1 {
		t.Fatalf("capabilities = %#v", capabilities)
	}
	capability := capabilities[0]
	if capability.State != tuning.StateKernelBlocked || capability.ReasonCode != "policy_state_unverified" {
		t.Fatalf("partial-policy capability = %+v", capability)
	}
}

func TestEPPRestoreAcceptsAlreadyRestoredValueWhenKernelRejectsWrites(t *testing.T) {
	const preferencePath = eppRoot + "/policy0/energy_performance_preference"
	store := eppStore(map[string]string{
		"policy0/energy_performance_available_preferences": "default performance power\n",
		"policy0/energy_performance_preference":            "default\n",
	})
	driver := New(store)
	if _, err := driver.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	op, err := driver.Prepare(context.Background(), tuning.Change{ID: tuning.ControlEPP, Requested: tuning.ChoiceValue("power")})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := op.Capture(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	store.Fail("write", preferencePath, errors.New("device or resource busy"))
	if _, err := op.Restore(context.Background(), snapshot); err != nil {
		t.Fatalf("already-restored value required an unnecessary write: %v", err)
	}
	if len(store.Writes) != 0 {
		t.Fatalf("already-restored value caused %d writes", len(store.Writes))
	}
}

func TestEPPApplySkipsWriteWhenRequestedValueIsAlreadyEffective(t *testing.T) {
	const preferencePath = eppRoot + "/policy0/energy_performance_preference"
	store := eppStore(map[string]string{
		"policy0/energy_performance_available_preferences": "default performance power\n",
		"policy0/energy_performance_preference":            "default\n",
	})
	driver := New(store)
	if _, err := driver.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	op, err := driver.Prepare(context.Background(), tuning.Change{ID: tuning.ControlEPP, Requested: tuning.ChoiceValue("default")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := op.Capture(context.Background()); err != nil {
		t.Fatal(err)
	}
	store.Fail("write", preferencePath, errors.New("device or resource busy"))
	if _, err := op.Apply(context.Background()); err != nil {
		t.Fatalf("idempotent apply attempted a write: %v", err)
	}
	if len(store.Writes) != 0 {
		t.Fatalf("idempotent apply caused %d writes", len(store.Writes))
	}
}

func TestWriteChoiceFailsOnEveryUnverifiedPath(t *testing.T) {
	const preferencePath = eppRoot + "/policy0/energy_performance_preference"
	tests := []struct {
		name           string
		configure      func(*scriptedChoiceStore)
		wantWriteCount int
	}{
		{
			name: "initial read",
			configure: func(store *scriptedChoiceStore) {
				store.failReadAt = 1
			},
		},
		{
			name: "changed-value write",
			configure: func(store *scriptedChoiceStore) {
				store.Fail("write", preferencePath, errors.New("device or resource busy"))
			},
		},
		{
			name: "post-write read",
			configure: func(store *scriptedChoiceStore) {
				store.failReadAt = 2
			},
			wantWriteCount: 1,
		},
		{
			name: "read-back mismatch",
			configure: func(store *scriptedChoiceStore) {
				store.secondRead = []byte("balance_power\n")
			},
			wantWriteCount: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &scriptedChoiceStore{MemoryStore: testkit.NewMemoryStore(map[string]string{preferencePath: "default\n"})}
			test.configure(store)
			if err := writeChoice(store, preferencePath, "power"); err == nil {
				t.Fatal("unverified choice was accepted")
			}
			if len(store.Writes) != test.wantWriteCount {
				t.Fatalf("writes = %d, want %d", len(store.Writes), test.wantWriteCount)
			}
		})
	}
}

type scriptedChoiceStore struct {
	*testkit.MemoryStore
	readCount  int
	failReadAt int
	secondRead []byte
}

func (store *scriptedChoiceStore) Read(path string) ([]byte, error) {
	store.readCount++
	if store.readCount == store.failReadAt {
		return nil, errors.New("read failed")
	}
	if store.readCount == 2 && store.secondRead != nil {
		return append([]byte(nil), store.secondRead...), nil
	}
	return store.MemoryStore.Read(path)
}

func eppStore(files map[string]string) *testkit.MemoryStore {
	prefixed := make(map[string]string, len(files))
	for name, value := range files {
		prefixed[eppRoot+"/"+name] = value
	}
	return testkit.NewMemoryStore(prefixed)
}

func assertPreference(t *testing.T, store *testkit.MemoryStore, policy, want string) {
	t.Helper()
	raw, err := store.Read(eppRoot + "/" + policy + "/energy_performance_preference")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(raw); got != want {
		t.Fatalf("%s preference = %q, want %q", policy, got, want)
	}
}
