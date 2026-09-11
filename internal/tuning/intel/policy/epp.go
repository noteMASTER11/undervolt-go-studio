package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/sysfs"
)

const eppRoot = "sys/devices/system/cpu/cpufreq"

type Driver struct {
	store sysfs.Store

	mu       sync.RWMutex
	policies []string
	choices  []string
}

func New(store sysfs.Store) *Driver {
	return &Driver{store: store}
}

func (driver *Driver) ID() string { return "intel.epp" }

func (driver *Driver) Probe(ctx context.Context) ([]tuning.Capability, error) {
	entries, err := driver.store.List(eppRoot)
	if err != nil {
		return nil, fmt.Errorf("epp: enumerate policies: %w", err)
	}
	var policies []string
	var common []string
	var current []string
	var revisionPaths []string
	performanceGovernorBlocked := false
	policyStateUnverified := false
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !strings.HasPrefix(entry, "policy") {
			continue
		}
		base := eppRoot + "/" + entry
		revisionPaths = append(revisionPaths, base+"/energy_performance_available_preferences", base+"/energy_performance_preference", base+"/scaling_governor", base+"/scaling_driver")
		scalingDriver, scalingDriverErr := driver.store.Read(base + "/scaling_driver")
		scalingGovernor, scalingGovernorErr := driver.store.Read(base + "/scaling_governor")
		if scalingDriverErr != nil || scalingGovernorErr != nil {
			policyStateUnverified = true
		} else if strings.TrimSpace(string(scalingDriver)) == "intel_pstate" && strings.TrimSpace(string(scalingGovernor)) == "performance" {
			performanceGovernorBlocked = true
		}
		availableRaw, availableErr := driver.store.Read(base + "/energy_performance_available_preferences")
		currentRaw, currentErr := driver.store.Read(base + "/energy_performance_preference")
		if availableErr != nil || currentErr != nil {
			policyStateUnverified = true
			continue
		}
		available := strings.Fields(string(availableRaw))
		if len(available) == 0 {
			continue
		}
		if common == nil {
			common = append([]string(nil), available...)
		} else {
			common = intersect(common, available)
		}
		policies = append(policies, base+"/energy_performance_preference")
		current = append(current, strings.TrimSpace(string(currentRaw)))
	}
	if len(policies) == 0 {
		return nil, nil
	}
	sort.Strings(policies)
	driver.mu.Lock()
	driver.policies = append([]string(nil), policies...)
	driver.choices = append([]string(nil), common...)
	driver.mu.Unlock()

	state := tuning.StateSupported
	reasonCode, reason := "", ""
	if performanceGovernorBlocked {
		state = tuning.StateKernelBlocked
		reasonCode = "intel_pstate_performance_governor"
		reason = "intel_pstate performance mode rejects energy-preference changes; select a balanced system power profile first"
	} else if policyStateUnverified {
		state = tuning.StateKernelBlocked
		reasonCode = "policy_state_unverified"
		reason = "CPU policy driver or governor state could not be verified; energy-preference editing is disabled"
	} else if len(common) == 0 {
		state = tuning.StateKernelBlocked
		reasonCode = "no_common_preference"
		reason = "CPU policies do not expose a common energy preference"
	}
	currentChoice := current[0]
	for _, choice := range current[1:] {
		if choice != currentChoice {
			currentChoice = "mixed"
			break
		}
	}
	return []tuning.Capability{{
		SourceRevision:    sysfs.Revision(driver.store, revisionPaths...),
		ID:                tuning.ControlEPP,
		Scope:             "All CPU policies",
		Label:             "Energy preference",
		Unit:              tuning.UnitChoice,
		State:             state,
		Current:           tuning.ChoiceValue(currentChoice),
		Choices:           append([]string(nil), common...),
		DriverID:          driver.ID(),
		ReasonCode:        reasonCode,
		Reason:            reason,
		RequiresPrivilege: true,
		ObservedAt:        time.Now(),
	}}, nil
}

func (driver *Driver) Prepare(ctx context.Context, change tuning.Change) (tuning.PreparedOperation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if change.ID != tuning.ControlEPP || change.Requested.Kind != tuning.ValueChoice {
		return nil, fmt.Errorf("epp: invalid change for %s", change.ID)
	}
	driver.mu.RLock()
	policies := append([]string(nil), driver.policies...)
	choices := append([]string(nil), driver.choices...)
	driver.mu.RUnlock()
	if len(policies) == 0 {
		return nil, fmt.Errorf("epp: no discovered CPU policies")
	}
	if !slices.Contains(choices, change.Requested.Choice) {
		return nil, fmt.Errorf("epp: unsupported preference %q", change.Requested.Choice)
	}
	return &operation{store: driver.store, paths: policies, requested: change.Requested.Choice}, nil
}

func (driver *Driver) Restore(ctx context.Context, id tuning.ControlID, raw json.RawMessage) (tuning.Value, error) {
	if id != tuning.ControlEPP {
		return tuning.Value{}, fmt.Errorf("epp: unsupported control %s", id)
	}
	driver.mu.RLock()
	policies := append([]string(nil), driver.policies...)
	driver.mu.RUnlock()
	return (&operation{store: driver.store, paths: policies}).Restore(ctx, raw)
}

type preferenceRecord struct {
	Path  string `json:"path"`
	Value string `json:"value"`
}

type operation struct {
	store     sysfs.Store
	paths     []string
	requested string
	snapshot  []preferenceRecord
}

func (operation *operation) ControlID() tuning.ControlID { return tuning.ControlEPP }
func (operation *operation) DriverID() string            { return "intel.epp" }
func (operation *operation) Order() int                  { return tuning.OrderPolicy }

func (operation *operation) Remaining(ctx context.Context) (tuning.Value, error) {
	if err := ctx.Err(); err != nil {
		return tuning.Value{}, err
	}
	return effectivePreference(operation.store, operation.paths)
}

func (operation *operation) Capture(ctx context.Context) (json.RawMessage, error) {
	records := make([]preferenceRecord, 0, len(operation.paths))
	for _, path := range operation.paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		value, err := operation.store.Read(path)
		if err != nil {
			return nil, err
		}
		records = append(records, preferenceRecord{Path: path, Value: strings.TrimSpace(string(value))})
	}
	operation.snapshot = append([]preferenceRecord(nil), records...)
	return json.Marshal(records)
}

func (operation *operation) Apply(ctx context.Context) (tuning.Value, error) {
	if len(operation.snapshot) != len(operation.paths) {
		return tuning.Value{}, fmt.Errorf("epp: operation must be captured before apply")
	}
	written := 0
	for index, path := range operation.paths {
		if err := ctx.Err(); err != nil {
			operation.rollback(written)
			return tuning.Value{}, err
		}
		if err := writeChoice(operation.store, path, operation.requested); err != nil {
			return tuning.Value{}, errors.Join(err, operation.rollback(written))
		}
		_ = index
		written++
	}
	return tuning.ChoiceValue(operation.requested), nil
}

func (operation *operation) Restore(ctx context.Context, raw json.RawMessage) (tuning.Value, error) {
	var records []preferenceRecord
	if err := json.Unmarshal(raw, &records); err != nil {
		return tuning.Value{}, err
	}
	allowed := make(map[string]struct{}, len(operation.paths))
	for _, path := range operation.paths {
		allowed[path] = struct{}{}
	}
	var restoreErr error
	for index := len(records) - 1; index >= 0; index-- {
		if err := ctx.Err(); err != nil {
			return tuning.Value{}, errors.Join(restoreErr, err)
		}
		record := records[index]
		if _, ok := allowed[record.Path]; !ok {
			return tuning.Value{}, fmt.Errorf("epp: unexpected snapshot path %s", record.Path)
		}
		if err := writeChoice(operation.store, record.Path, record.Value); err != nil {
			restoreErr = errors.Join(restoreErr, err)
		}
	}
	if restoreErr != nil {
		return tuning.Value{}, restoreErr
	}
	return effectivePreference(operation.store, operation.paths)
}

func (operation *operation) rollback(count int) error {
	var rollbackErr error
	for index := count - 1; index >= 0; index-- {
		record := operation.snapshot[index]
		rollbackErr = errors.Join(rollbackErr, writeChoice(operation.store, record.Path, record.Value))
	}
	return rollbackErr
}

func writeChoice(store sysfs.Store, path, choice string) error {
	current, err := store.Read(path)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(current)) == choice {
		return nil
	}
	if err := store.Write(path, []byte(choice)); err != nil {
		return err
	}
	raw, err := store.Read(path)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(raw)) != choice {
		return fmt.Errorf("epp: read-back mismatch at %s", path)
	}
	return nil
}

func effectivePreference(store sysfs.Store, paths []string) (tuning.Value, error) {
	current := ""
	for _, path := range paths {
		raw, err := store.Read(path)
		if err != nil {
			return tuning.Value{}, err
		}
		choice := strings.TrimSpace(string(raw))
		if current == "" {
			current = choice
		} else if current != choice {
			return tuning.ChoiceValue("mixed"), nil
		}
	}
	return tuning.ChoiceValue(current), nil
}

func intersect(left, right []string) []string {
	result := make([]string, 0, len(left))
	for _, value := range left {
		if slices.Contains(right, value) {
			result = append(result, value)
		}
	}
	return result
}
