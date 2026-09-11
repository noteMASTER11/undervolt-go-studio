package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/intel"
)

func TestEmitReportCreatesMissingOutputParent(t *testing.T) {
	output := filepath.Join(t.TempDir(), "reports", "smoke", "report.json")
	report := smokeReport{Mode: "read-only"}

	if err := emitReport(io.Discard, output, report); err != nil {
		t.Fatalf("emit report: %v", err)
	}
	encoded, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if !bytes.Contains(encoded, []byte(`"mode": "read-only"`)) {
		t.Fatalf("report=%s", encoded)
	}
}

func TestCaseValueMapsRequireEveryRequestedControl(t *testing.T) {
	changes := []tuning.Change{
		{ID: tuning.ControlPL1, Requested: tuning.NumericValue(44)},
		{ID: tuning.ControlPL2, Requested: tuning.NumericValue(55)},
	}
	complete := map[tuning.ControlID]tuning.Value{
		tuning.ControlPL1: tuning.NumericValue(44),
		tuning.ControlPL2: tuning.NumericValue(55),
	}
	for _, phase := range []string{"stock", "effective", "restored"} {
		if err := requireValuesForChanges(phase, complete, changes); err != nil {
			t.Fatalf("%s map rejected: %v", phase, err)
		}
		incomplete := map[tuning.ControlID]tuning.Value{tuning.ControlPL1: tuning.NumericValue(44)}
		err := requireValuesForChanges(phase, incomplete, changes)
		if err == nil || !strings.Contains(err.Error(), string(tuning.ControlPL2)) {
			t.Fatalf("%s missing control err=%v", phase, err)
		}
	}
}

func TestRecoveryFailurePersistsReportBeforeMutation(t *testing.T) {
	output := filepath.Join(t.TempDir(), "reports", "recovery.json")
	recoveryErr := errors.New("unresolved prior transaction")
	engine := tuning.NewEngine(tuning.CapabilitySet{MachineID: "test-machine"}, recoveryFailureStore{err: recoveryErr})
	report := smokeReport{Mode: "conservative-mutation"}

	err := recoverBeforeMutation(context.Background(), engine, &report, output)
	if !errors.Is(err, recoveryErr) {
		t.Fatalf("recovery error=%v", err)
	}
	encoded, readErr := os.ReadFile(output)
	if readErr != nil {
		t.Fatalf("read recovery report: %v", readErr)
	}
	var saved smokeReport
	if err := json.Unmarshal(encoded, &saved); err != nil {
		t.Fatalf("decode recovery report: %v", err)
	}
	if !strings.Contains(saved.RecoveryError, recoveryErr.Error()) {
		t.Fatalf("recovery error was not persisted: %+v", saved)
	}
}

func TestExerciseCaseDefersRollbackAfterApplyPanic(t *testing.T) {
	driver := &exerciseCaseTestDriver{}
	capabilities := exerciseCaseTestCapabilities(driver.ID())
	engine := tuning.NewEngine(capabilities, &exerciseCaseRecoveryStore{}, driver)
	candidate := smokeCase{Name: "power", Changes: []tuning.Change{{ID: tuning.ControlPL1, Requested: tuning.NumericValue(43)}}}

	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("rediscovery panic was not propagated")
		}
		if driver.restores != 1 {
			t.Fatalf("restores=%d, want one deferred rollback", driver.restores)
		}
	}()
	exerciseCaseWithRediscovery(context.Background(), candidate, capabilities, engine, func(context.Context) (tuning.CapabilitySet, error) {
		panic("rediscovery failed unexpectedly")
	})
}

func TestExerciseCaseRollsBackOnlyOnceAfterExplicitRestore(t *testing.T) {
	driver := &exerciseCaseTestDriver{}
	capabilities := exerciseCaseTestCapabilities(driver.ID())
	engine := tuning.NewEngine(capabilities, &exerciseCaseRecoveryStore{}, driver)
	candidate := smokeCase{Name: "power", Changes: []tuning.Change{{ID: tuning.ControlPL1, Requested: tuning.NumericValue(43)}}}
	result := exerciseCaseWithRediscovery(context.Background(), candidate, capabilities, engine, func(context.Context) (tuning.CapabilitySet, error) {
		return capabilities, nil
	})
	if result.Error != "" {
		t.Fatalf("exercise case: %s", result.Error)
	}
	if driver.restores != 1 {
		t.Fatalf("restores=%d, want one explicit rollback", driver.restores)
	}
}

type recoveryFailureStore struct{ err error }

func (store recoveryFailureStore) Load() (tuning.RecoveryRecord, error) {
	return tuning.RecoveryRecord{}, store.err
}
func (recoveryFailureStore) Save(tuning.RecoveryRecord) error { return nil }
func (recoveryFailureStore) Remove() error                    { return nil }

type exerciseCaseRecoveryStore struct{}

func (*exerciseCaseRecoveryStore) Load() (tuning.RecoveryRecord, error) {
	return tuning.RecoveryRecord{}, os.ErrNotExist
}
func (*exerciseCaseRecoveryStore) Save(tuning.RecoveryRecord) error { return nil }
func (*exerciseCaseRecoveryStore) Remove() error                    { return nil }

type exerciseCaseTestDriver struct{ restores int }

func (*exerciseCaseTestDriver) ID() string { return "test-driver" }
func (*exerciseCaseTestDriver) Probe(context.Context) ([]tuning.Capability, error) {
	return nil, nil
}
func (driver *exerciseCaseTestDriver) Prepare(_ context.Context, change tuning.Change) (tuning.PreparedOperation, error) {
	return &exerciseCaseTestOperation{driver: driver, change: change}, nil
}
func (*exerciseCaseTestDriver) Restore(context.Context, tuning.ControlID, json.RawMessage) (tuning.Value, error) {
	return tuning.NumericValue(44), nil
}

type exerciseCaseTestOperation struct {
	driver *exerciseCaseTestDriver
	change tuning.Change
}

func (operation *exerciseCaseTestOperation) ControlID() tuning.ControlID { return operation.change.ID }
func (*exerciseCaseTestOperation) DriverID() string                      { return "test-driver" }
func (*exerciseCaseTestOperation) Order() int                            { return tuning.OrderPower }
func (*exerciseCaseTestOperation) Capture(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`44`), nil
}
func (operation *exerciseCaseTestOperation) Apply(context.Context) (tuning.Value, error) {
	return operation.change.Requested, nil
}
func (operation *exerciseCaseTestOperation) Restore(context.Context, json.RawMessage) (tuning.Value, error) {
	operation.driver.restores++
	return tuning.NumericValue(44), nil
}

func exerciseCaseTestCapabilities(driverID string) tuning.CapabilitySet {
	return tuning.CapabilitySet{
		Generation: "test-generation",
		MachineID:  "test-machine",
		Capabilities: []tuning.Capability{{
			ID:       tuning.ControlPL1,
			DriverID: driverID,
			State:    tuning.StateSupported,
			Unit:     tuning.UnitWatt,
			Current:  tuning.NumericValue(44),
			Range:    &tuning.NumericRange{Minimum: 15, Maximum: 55, Step: 1},
		}},
	}
}

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
