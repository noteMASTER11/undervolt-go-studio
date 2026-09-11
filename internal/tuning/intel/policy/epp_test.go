package policy

import (
	"context"
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
