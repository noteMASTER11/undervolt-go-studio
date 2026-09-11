// Command tuning-smoke performs opt-in validation of the Intel tuning backend.
// Its default mode is strictly read-only; mutation is guarded for one reviewed CPU.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/intel"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/intel/msr"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/intel/policy"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/intel/powercap"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/intel/thermal"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/sysfs"
)

const mutationConfirmation = "I UNDERSTAND TEMPORARY CPU TUNING"

var (
	ErrConfirmationRequired = errors.New("exact mutation confirmation is required")
	ErrTargetCPURequired    = errors.New("mutation is restricted to the reviewed Core Ultra 9 275HX signature")
	ErrRootRequired         = errors.New("mutation requires root privileges")
	ErrOutputRequired       = errors.New("mutation requires a saved output report path")
	ErrProbeRequired        = errors.New("mutation requires a successful read-only probe in this process")
)

type options struct {
	Mutate  bool
	Confirm string
	Output  string
}

type smokeCase struct {
	Name    string
	Changes []tuning.Change
}

type caseReport struct {
	Name      string                            `json:"name"`
	Stock     map[tuning.ControlID]tuning.Value `json:"stock"`
	Requested map[tuning.ControlID]tuning.Value `json:"requested"`
	Effective map[tuning.ControlID]tuning.Value `json:"effective,omitempty"`
	Restored  map[tuning.ControlID]tuning.Value `json:"restored,omitempty"`
	Events    []tuning.Event                    `json:"events"`
	Error     string                            `json:"error,omitempty"`
}

type smokeReport struct {
	CreatedAt     time.Time            `json:"created_at"`
	Mode          string               `json:"mode"`
	Identity      intel.Identity       `json:"identity"`
	Topology      []intel.Core         `json:"topology,omitempty"`
	TopologyError string               `json:"topology_error,omitempty"`
	Capabilities  tuning.CapabilitySet `json:"capabilities"`
	ProbeError    string               `json:"probe_error,omitempty"`
	RecoveryError string               `json:"recovery_error,omitempty"`
	Cases         []caseReport         `json:"cases,omitempty"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Geteuid); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string, stdout io.Writer, euid func() int) error {
	flags := flag.NewFlagSet("tuning-smoke", flag.ContinueOnError)
	flags.SetOutput(stdout)
	var config options
	flags.BoolVar(&config.Mutate, "mutate", false, "temporarily apply and restore conservative validation changes")
	flags.StringVar(&config.Confirm, "confirm", "", "exact confirmation phrase required for --mutate")
	flags.StringVar(&config.Output, "output", "", "JSON report path (required for --mutate)")
	if err := flags.Parse(arguments); err != nil {
		return err
	}

	identity, err := intel.DetectIdentity()
	if err != nil {
		return err
	}
	if err := validateMutationGate(config.Mutate, config.Confirm, identity); err != nil {
		return err
	}
	if err := validateMutationEnvironment(config.Mutate, euid(), config.Output, true); err != nil && !errors.Is(err, ErrProbeRequired) {
		return err
	}

	store := sysfs.RootStore{Root: "/"}
	topology, topologyErr := intel.DetectTopology(store, intel.CPUIDOnCPU)
	drivers := systemDrivers(store, identity, topology)
	machineID := fmt.Sprintf("%s-%d-%x-%d", identity.Vendor, identity.Family, identity.Model, identity.Stepping)
	discoverer := tuning.NewDiscoverer(machineID, drivers...)
	report := smokeReport{
		CreatedAt: time.Now(), Identity: identity, Topology: topology,
	}
	if config.Mutate {
		report.Mode = "conservative-mutation"
	} else {
		report.Mode = "read-only"
	}
	if topologyErr != nil {
		report.TopologyError = topologyErr.Error()
	}
	var engine *tuning.Engine
	if config.Mutate {
		bootID, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
		if err != nil {
			return fmt.Errorf("read boot ID: %w", err)
		}
		engine = tuning.NewEngine(tuning.CapabilitySet{MachineID: machineID}, tuning.FileRecoveryStore{}, drivers...)
		engine.BootID = strings.TrimSpace(string(bootID))
		if err := recoverBeforeMutation(context.Background(), engine, &report, config.Output); err != nil {
			return err
		}
	}
	capabilities, probeErr := discover(context.Background(), discoverer)
	report.Capabilities = capabilities
	if probeErr != nil {
		report.ProbeError = probeErr.Error()
	}
	probeComplete := capabilities.Generation != "" && probeErr == nil
	if err := validateMutationEnvironment(config.Mutate, euid(), config.Output, probeComplete); err != nil {
		_ = emitReport(stdout, config.Output, report)
		return err
	}
	initialOutput := stdout
	if config.Mutate {
		initialOutput = io.Discard
	}
	if err := emitReport(initialOutput, config.Output, report); err != nil {
		return fmt.Errorf("save read-only report before mutation: %w", err)
	}
	if !config.Mutate {
		return nil
	}
	engine.Capabilities = capabilities
	for _, candidate := range conservativeCases(capabilities) {
		result := exerciseCase(context.Background(), candidate, capabilities, engine, discoverer)
		report.Cases = append(report.Cases, result)
		if saveErr := emitReport(io.Discard, config.Output, report); saveErr != nil {
			return saveErr
		}
		if result.Error != "" {
			return fmt.Errorf("%s: %s", result.Name, result.Error)
		}
	}
	return emitReport(stdout, config.Output, report)
}

func recoverBeforeMutation(ctx context.Context, engine *tuning.Engine, report *smokeReport, output string) error {
	if _, err := engine.Recover(ctx); err != nil {
		report.RecoveryError = err.Error()
		if reportErr := emitReport(io.Discard, output, *report); reportErr != nil {
			return errors.Join(fmt.Errorf("recover previous mutation: %w", err), fmt.Errorf("persist recovery failure report: %w", reportErr))
		}
		return fmt.Errorf("recover previous mutation: %w", err)
	}
	return nil
}

func validateMutationGate(mutate bool, confirmation string, identity intel.Identity) error {
	if !mutate {
		return nil
	}
	if confirmation != mutationConfirmation {
		return ErrConfirmationRequired
	}
	if identity.Vendor != "GenuineIntel" || identity.Family != 6 || identity.Model != 0xc6 || identity.Stepping != 2 {
		return fmt.Errorf("%w: detected %s family %d model %#x stepping %d", ErrTargetCPURequired, identity.Vendor, identity.Family, identity.Model, identity.Stepping)
	}
	return nil
}

func validateMutationEnvironment(mutate bool, euid int, output string, probed bool) error {
	if !mutate {
		return nil
	}
	if euid != 0 {
		return ErrRootRequired
	}
	if strings.TrimSpace(output) == "" {
		return ErrOutputRequired
	}
	if !probed {
		return ErrProbeRequired
	}
	return nil
}

func systemDrivers(store sysfs.Store, identity intel.Identity, topology []intel.Core) []tuning.Driver {
	pCores := 0
	for _, core := range topology {
		if core.Type == intel.CorePerformance {
			pCores++
		}
	}
	return []tuning.Driver{
		powercap.New(store), policy.New(store), thermal.New(store),
		msr.NewDriver(msr.LinuxDevice{}, identity, pCores),
	}
}

func discover(ctx context.Context, discoverer *tuning.Discoverer) (tuning.CapabilitySet, error) {
	var final tuning.DiscoveryResult
	for result := range discoverer.Discover(ctx) {
		final = result
	}
	if !final.Complete {
		return final.Set, errors.Join(ctx.Err(), final.Err, errors.New("capability discovery did not complete"))
	}
	return final.Set, final.Err
}

func conservativeCases(set tuning.CapabilitySet) []smokeCase {
	byID := make(map[tuning.ControlID]tuning.Capability, len(set.Capabilities))
	allByID := make(map[tuning.ControlID]tuning.Capability, len(set.Capabilities))
	for _, capability := range set.Capabilities {
		allByID[capability.ID] = capability
		if capability.State == tuning.StateSupported {
			byID[capability.ID] = capability
		}
	}
	var cases []smokeCase
	var power []tuning.Change
	effectivePL1 := math.Inf(-1)
	if capability, ok := allByID[tuning.ControlPL1]; ok && capability.Current.Kind == tuning.ValueNumeric {
		effectivePL1 = capability.Current.Number
	}
	if capability, ok := byID[tuning.ControlPL1]; ok && capability.Range != nil && capability.Current.Kind == tuning.ValueNumeric {
		effectivePL1 = lowerNumeric(capability, capability.Range.Step)
		power = append(power, tuning.Change{ID: capability.ID, Requested: tuning.NumericValue(effectivePL1)})
	}
	if capability, ok := byID[tuning.ControlPL2]; ok && capability.Range != nil && capability.Current.Kind == tuning.ValueNumeric {
		target := lowerNumeric(capability, capability.Range.Step)
		if !math.IsInf(effectivePL1, -1) {
			target = math.Max(target, effectivePL1)
		}
		power = append(power, tuning.Change{ID: capability.ID, Requested: tuning.NumericValue(target)})
	}
	if len(power) > 0 {
		cases = append(cases, smokeCase{Name: "power-limits", Changes: power})
	}
	if capability, ok := byID[tuning.ControlThermalLimit]; ok && capability.Range != nil && capability.Current.Kind == tuning.ValueNumeric {
		cases = append(cases, smokeCase{Name: "thermal-limit", Changes: []tuning.Change{{ID: capability.ID, Requested: tuning.NumericValue(lowerNumeric(capability, capability.Range.Step))}}})
	}
	if capability, ok := byID[tuning.ControlEPP]; ok && len(capability.Choices) > 0 {
		preference := capability.Current.Choice
		for _, candidate := range []string{"power", "balance_power", "balance_performance", "performance"} {
			if slices.Contains(capability.Choices, candidate) {
				preference = candidate
				break
			}
		}
		cases = append(cases, smokeCase{Name: "energy-policy", Changes: []tuning.Change{{ID: capability.ID, Requested: tuning.ChoiceValue(preference)}}})
	}
	if capability, ok := byID[tuning.ControlRatioPCore]; ok && capability.Current.Kind == tuning.ValueVector && len(capability.Current.Vector) > 0 {
		ratios := append([]float64(nil), capability.Current.Vector...)
		for index := range ratios {
			ratios[index] = math.Max(1, ratios[index]-1)
		}
		cases = append(cases, smokeCase{Name: "p-core-ratio", Changes: []tuning.Change{{ID: capability.ID, Requested: tuning.VectorValue(ratios)}}})
	}
	var voltages []tuning.Change
	for _, id := range []tuning.ControlID{tuning.ControlVoltageCore, tuning.ControlVoltageCache} {
		if capability, ok := byID[id]; ok && capability.Range != nil && capability.Current.Kind == tuning.ValueNumeric {
			voltages = append(voltages, tuning.Change{ID: id, Requested: tuning.NumericValue(lowerNumeric(capability, 10))})
		}
	}
	if len(voltages) > 0 {
		cases = append(cases, smokeCase{Name: "voltage-offsets", Changes: voltages})
	}
	return cases
}

func lowerNumeric(capability tuning.Capability, amount float64) float64 {
	return math.Max(capability.Range.Minimum, capability.Current.Number-math.Abs(amount))
}

func exerciseCase(ctx context.Context, candidate smokeCase, capabilities tuning.CapabilitySet, engine *tuning.Engine, discoverer *tuning.Discoverer) caseReport {
	return exerciseCaseWithRediscovery(ctx, candidate, capabilities, engine, func(ctx context.Context) (tuning.CapabilitySet, error) {
		return discover(ctx, discoverer)
	})
}

func exerciseCaseWithRediscovery(ctx context.Context, candidate smokeCase, capabilities tuning.CapabilitySet, engine *tuning.Engine, rediscover func(context.Context) (tuning.CapabilitySet, error)) (result caseReport) {
	result = caseReport{Name: candidate.Name, Stock: valuesForChanges(capabilities, candidate.Changes), Requested: requestedValues(candidate.Changes)}
	if err := requireValuesForChanges("stock", result.Stock, candidate.Changes); err != nil {
		result.Error = err.Error()
		return result
	}
	changeSet := tuning.ChangeSet{Generation: capabilities.Generation, MachineID: capabilities.MachineID, Changes: candidate.Changes}
	result.Events = append(result.Events, tuning.Event{Time: time.Now(), Kind: "applying", Message: "Capturing stock state and applying temporary smoke-test values"})
	active, _, err := engine.Apply(ctx, changeSet)
	if err != nil {
		result.Error = err.Error()
		result.Events = append(result.Events, tuning.Event{Time: time.Now(), Kind: "failed", Message: "Apply failed", Detail: err.Error()})
		return result
	}
	rollbackAttempted := false
	var rollbackErr error
	rollback := func() error {
		if rollbackAttempted {
			return rollbackErr
		}
		rollbackAttempted = true
		rollbackErr = active.Rollback(context.WithoutCancel(ctx))
		return rollbackErr
	}
	defer func() { _ = rollback() }()
	result.Events = append(result.Events, tuning.Event{Time: time.Now(), Kind: "applied", Message: "Temporary values applied with verified read-back"})
	effectiveSet, effectiveErr := rediscover(ctx)
	result.Effective = valuesForChanges(effectiveSet, candidate.Changes)
	effectiveErr = errors.Join(effectiveErr, requireValuesForChanges("effective", result.Effective, candidate.Changes))
	rollbackErr = rollback()
	if rollbackErr == nil {
		result.Events = append(result.Events, tuning.Event{Time: time.Now(), Kind: "restored", Message: "Stock values restored"})
	} else {
		result.Events = append(result.Events, tuning.Event{Time: time.Now(), Kind: "rollback_incomplete", Message: "Stock restore failed", Detail: rollbackErr.Error()})
	}
	restoredSet, restoredErr := rediscover(context.WithoutCancel(ctx))
	result.Restored = valuesForChanges(restoredSet, candidate.Changes)
	restoredErr = errors.Join(restoredErr, requireValuesForChanges("restored", result.Restored, candidate.Changes))
	restoreVerifyErr := verifyRestored(result.Stock, result.Restored)
	if err := errors.Join(effectiveErr, rollbackErr, restoredErr, restoreVerifyErr); err != nil {
		result.Error = err.Error()
	}
	return result
}

func valuesForChanges(set tuning.CapabilitySet, changes []tuning.Change) map[tuning.ControlID]tuning.Value {
	wanted := make(map[tuning.ControlID]struct{}, len(changes))
	for _, change := range changes {
		wanted[change.ID] = struct{}{}
	}
	values := make(map[tuning.ControlID]tuning.Value, len(wanted))
	for _, capability := range set.Capabilities {
		if _, ok := wanted[capability.ID]; ok {
			values[capability.ID] = capability.Current
		}
	}
	return values
}

func requestedValues(changes []tuning.Change) map[tuning.ControlID]tuning.Value {
	values := make(map[tuning.ControlID]tuning.Value, len(changes))
	for _, change := range changes {
		values[change.ID] = change.Requested
	}
	return values
}

func requireValuesForChanges(phase string, values map[tuning.ControlID]tuning.Value, changes []tuning.Change) error {
	for _, change := range changes {
		if _, ok := values[change.ID]; !ok {
			return fmt.Errorf("%s rediscovery: %s is missing", phase, change.ID)
		}
	}
	return nil
}

func verifyRestored(stock, restored map[tuning.ControlID]tuning.Value) error {
	for id, want := range stock {
		got, ok := restored[id]
		if !ok {
			return fmt.Errorf("restore verification: %s is missing", id)
		}
		if want.Kind != got.Kind {
			return fmt.Errorf("restore verification: %s changed kind from %s to %s", id, want.Kind, got.Kind)
		}
		switch want.Kind {
		case tuning.ValueNumeric:
			if math.Abs(want.Number-got.Number) > 0.001 {
				return fmt.Errorf("restore verification: %s stock %.3f, restored %.3f", id, want.Number, got.Number)
			}
		case tuning.ValueChoice:
			if want.Choice != got.Choice {
				return fmt.Errorf("restore verification: %s stock %q, restored %q", id, want.Choice, got.Choice)
			}
		case tuning.ValueVector:
			if len(want.Vector) != len(got.Vector) {
				return fmt.Errorf("restore verification: %s vector length changed", id)
			}
			for index := range want.Vector {
				if math.Abs(want.Vector[index]-got.Vector[index]) > 0.001 {
					return fmt.Errorf("restore verification: %s ratio %d stock %.3f, restored %.3f", id, index, want.Vector[index], got.Vector[index])
				}
			}
		default:
			return fmt.Errorf("restore verification: %s has unsupported value kind %s", id, want.Kind)
		}
	}
	return nil
}

func emitReport(stdout io.Writer, output string, report smokeReport) error {
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if stdout != io.Discard {
		if _, err := stdout.Write(encoded); err != nil {
			return err
		}
	}
	if strings.TrimSpace(output) == "" {
		return nil
	}
	directory := filepath.Dir(output)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create report directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".tuning-smoke-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	keep := true
	defer func() {
		_ = temporary.Close()
		if keep {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o644); err != nil {
		return err
	}
	if _, err := temporary.Write(encoded); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, output); err != nil {
		return err
	}
	keep = false
	return nil
}
