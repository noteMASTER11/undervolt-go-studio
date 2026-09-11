package main

import (
	"errors"
	"testing"

	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/intel"
)

func TestMutatingSmokeRequiresExactConfirmation(t *testing.T) {
	identity := intel.Identity{Vendor: "GenuineIntel", Family: 6, Model: 0xc6, Stepping: 2}
	for _, confirmation := range []string{"", "yes", "I understand", "I UNDERSTAND TEMPORARY CPU TUNING "} {
		if err := validateMutationGate(true, confirmation, identity); !errors.Is(err, ErrConfirmationRequired) {
			t.Fatalf("confirmation=%q err=%v", confirmation, err)
		}
	}
	if err := validateMutationGate(true, mutationConfirmation, identity); err != nil {
		t.Fatalf("exact confirmation rejected: %v", err)
	}
}

func TestMutatingSmokeRejectsEveryOtherProcessor(t *testing.T) {
	for _, identity := range []intel.Identity{
		{Vendor: "AuthenticAMD", Family: 6, Model: 0xc6, Stepping: 2},
		{Vendor: "GenuineIntel", Family: 6, Model: 0xb7, Stepping: 2},
		{Vendor: "GenuineIntel", Family: 6, Model: 0xc6, Stepping: 1},
	} {
		if err := validateMutationGate(true, mutationConfirmation, identity); !errors.Is(err, ErrTargetCPURequired) {
			t.Fatalf("identity=%+v err=%v", identity, err)
		}
	}
}

func TestMutatingSmokeRequiresRootSavedReportAndCompletedProbe(t *testing.T) {
	checks := []struct {
		name   string
		euid   int
		output string
		probed bool
		want   error
	}{
		{name: "root", euid: 1000, output: "report.json", probed: true, want: ErrRootRequired},
		{name: "output", euid: 0, probed: true, want: ErrOutputRequired},
		{name: "probe", euid: 0, output: "report.json", want: ErrProbeRequired},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := validateMutationEnvironment(true, check.euid, check.output, check.probed); !errors.Is(err, check.want) {
				t.Fatalf("error=%v, want %v", err, check.want)
			}
		})
	}
	if err := validateMutationEnvironment(false, 1000, "", false); err != nil {
		t.Fatalf("read-only mode was gated: %v", err)
	}
}

func TestConservativeCasesAreOrderedAndNeverRaiseLimits(t *testing.T) {
	set := tuning.CapabilitySet{Capabilities: []tuning.Capability{
		{ID: tuning.ControlPL1, State: tuning.StateSupported, Current: tuning.NumericValue(44), Range: &tuning.NumericRange{Minimum: 15, Maximum: 55, Step: 1}},
		{ID: tuning.ControlPL2, State: tuning.StateSupported, Current: tuning.NumericValue(60), Range: &tuning.NumericRange{Minimum: 15, Maximum: 80, Step: 1}},
		{ID: tuning.ControlThermalLimit, State: tuning.StateSupported, Current: tuning.NumericValue(100), Range: &tuning.NumericRange{Minimum: 70, Maximum: 105, Step: 1}},
		{ID: tuning.ControlEPP, State: tuning.StateSupported, Current: tuning.ChoiceValue("performance"), Choices: []string{"performance", "balance_performance", "balance_power", "power"}},
		{ID: tuning.ControlRatioPCore, State: tuning.StateSupported, Current: tuning.VectorValue([]float64{57, 56, 55})},
		{ID: tuning.ControlVoltageCore, State: tuning.StateSupported, Current: tuning.NumericValue(0), Range: &tuning.NumericRange{Minimum: -250, Maximum: 0, Step: 1 / 1.024}},
		{ID: tuning.ControlVoltageCache, State: tuning.StateSupported, Current: tuning.NumericValue(0), Range: &tuning.NumericRange{Minimum: -250, Maximum: 0, Step: 1 / 1.024}},
	}}
	cases := conservativeCases(set)
	wantNames := []string{"power-limits", "thermal-limit", "energy-policy", "p-core-ratio", "voltage-offsets"}
	if len(cases) != len(wantNames) {
		t.Fatalf("cases=%v", cases)
	}
	for index, name := range wantNames {
		if cases[index].Name != name {
			t.Fatalf("case %d=%q, want %q", index, cases[index].Name, name)
		}
	}
	for _, change := range cases[0].Changes {
		if change.ID == tuning.ControlPL1 && change.Requested.Number > 44 || change.ID == tuning.ControlPL2 && change.Requested.Number > 60 {
			t.Fatalf("power case raised a limit: %+v", change)
		}
	}
	if got := cases[3].Changes[0].Requested.Vector; got[0] != 56 || got[1] != 55 || got[2] != 54 {
		t.Fatalf("ratio request=%v", got)
	}
	for _, change := range cases[4].Changes {
		if change.Requested.Number >= 0 || change.Requested.Number < -10.1 {
			t.Fatalf("voltage request=%+v", change.Requested)
		}
	}
}

func TestConservativePL2NeverDropsBelowEffectivePL1(t *testing.T) {
	set := tuning.CapabilitySet{Capabilities: []tuning.Capability{
		{ID: tuning.ControlPL1, State: tuning.StateReadOnly, Current: tuning.NumericValue(44)},
		{ID: tuning.ControlPL2, State: tuning.StateSupported, Current: tuning.NumericValue(44), Range: &tuning.NumericRange{Minimum: 15, Maximum: 80, Step: 1}},
	}}
	cases := conservativeCases(set)
	if len(cases) != 1 || len(cases[0].Changes) != 1 {
		t.Fatalf("cases=%+v", cases)
	}
	if got := cases[0].Changes[0].Requested.Number; got != 44 {
		t.Fatalf("PL2 request=%v, want 44 to preserve PL1 <= PL2", got)
	}
}

func TestRestoredValuesMustMatchStock(t *testing.T) {
	stock := map[tuning.ControlID]tuning.Value{tuning.ControlPL1: tuning.NumericValue(44)}
	if err := verifyRestored(stock, map[tuning.ControlID]tuning.Value{tuning.ControlPL1: tuning.NumericValue(43)}); err == nil {
		t.Fatal("restore mismatch was accepted")
	}
	if err := verifyRestored(stock, map[tuning.ControlID]tuning.Value{tuning.ControlPL1: tuning.NumericValue(44)}); err != nil {
		t.Fatalf("matching restore rejected: %v", err)
	}
}
