package powercap

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
)

func (driver *Driver) Probe(ctx context.Context) ([]tuning.Capability, error) {
	entries, err := driver.store.List(root)
	if err != nil {
		return nil, fmt.Errorf("powercap: enumerate zones: %w", err)
	}
	found := make(map[tuning.ControlID][]constraint)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !isTopLevelZone(entry) {
			continue
		}
		zonePath := root + "/" + entry
		name, readErr := driver.readTrimmed(zonePath + "/name")
		if readErr != nil || !strings.HasPrefix(name, "package-") {
			continue
		}
		if err := driver.discoverZone(ctx, zonePath, entry, found); err != nil {
			return nil, err
		}
	}

	for id := range found {
		sort.Slice(found[id], func(i, j int) bool {
			left, right := found[id][i], found[id][j]
			if sourceRank(left.zone) != sourceRank(right.zone) {
				return sourceRank(left.zone) < sourceRank(right.zone)
			}
			return left.path < right.path
		})
	}
	driver.mu.Lock()
	driver.zones = found
	driver.mu.Unlock()

	ids := []tuning.ControlID{tuning.ControlPL1, tuning.ControlPL2, tuning.ControlTau}
	capabilities := make([]tuning.Capability, 0, len(ids))
	for _, id := range ids {
		if sources := found[id]; len(sources) > 0 {
			capabilities = append(capabilities, capabilityFor(id, sources))
		}
	}
	return capabilities, nil
}

func (driver *Driver) discoverZone(ctx context.Context, zonePath, zone string, found map[tuning.ControlID][]constraint) error {
	entries, err := driver.store.List(zonePath)
	if err != nil {
		return fmt.Errorf("powercap: enumerate %s: %w", zone, err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !strings.HasPrefix(entry, "constraint_") || !strings.HasSuffix(entry, "_name") {
			continue
		}
		prefix := strings.TrimSuffix(entry, "name")
		semanticName, err := driver.readTrimmed(zonePath + "/" + entry)
		if err != nil {
			continue
		}
		switch semanticName {
		case "long_term":
			driver.addSource(found, tuning.ControlPL1, zone, zonePath+"/"+prefix+"power_limit_uw", zonePath+"/"+prefix+"max_power_uw", 1e6)
			driver.addSource(found, tuning.ControlTau, zone, zonePath+"/"+prefix+"time_window_us", zonePath+"/"+prefix+"max_time_window_us", 1e6)
		case "short_term":
			driver.addSource(found, tuning.ControlPL2, zone, zonePath+"/"+prefix+"power_limit_uw", zonePath+"/"+prefix+"max_power_uw", 1e6)
		}
	}
	return nil
}

func (driver *Driver) addSource(found map[tuning.ControlID][]constraint, id tuning.ControlID, zone, valuePath, maximumPath string, scale float64) {
	current, err := driver.readPositiveOrZero(valuePath)
	if err != nil {
		return
	}
	var maximum *int64
	if parsed, maxErr := driver.readPositiveOrZero(maximumPath); maxErr == nil && parsed > 0 {
		maximum = &parsed
	}
	found[id] = append(found[id], constraint{
		path:    valuePath,
		current: current,
		maximum: maximum,
		scale:   scale,
		zone:    zone,
	})
}

func capabilityFor(id tuning.ControlID, sources []constraint) tuning.Capability {
	current := math.Inf(1)
	maximum := math.Inf(1)
	hasMaximum := true
	for _, source := range sources {
		current = math.Min(current, float64(source.current)/source.scale)
		if source.maximum == nil {
			hasMaximum = false
		} else {
			maximum = math.Min(maximum, float64(*source.maximum)/source.scale)
		}
	}
	capability := tuning.Capability{
		ID:                id,
		Scope:             "CPU package",
		State:             tuning.StateReadOnly,
		Current:           tuning.NumericValue(current),
		DriverID:          "intel.powercap",
		RequiresPrivilege: true,
		ObservedAt:        time.Now(),
	}
	switch id {
	case tuning.ControlPL1:
		capability.Label, capability.Unit = "Sustained power limit", tuning.UnitWatt
	case tuning.ControlPL2:
		capability.Label, capability.Unit = "Short boost power limit", tuning.UnitWatt
	case tuning.ControlTau:
		capability.Label, capability.Unit = "Turbo time window", tuning.UnitSecond
	}
	if hasMaximum && !math.IsInf(maximum, 1) {
		capability.State = tuning.StateSupported
		capability.Range = &tuning.NumericRange{Minimum: 0, Maximum: maximum, Step: 1 / sources[0].scale}
	} else {
		capability.ReasonCode = "unknown_upper_bound"
		capability.Reason = "The kernel does not report a writable upper bound"
	}
	return capability
}

func (driver *Driver) readTrimmed(path string) (string, error) {
	value, err := driver.store.Read(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(value)), nil
}

func (driver *Driver) readPositiveOrZero(path string) (int64, error) {
	value, err := driver.readTrimmed(path)
	if err != nil {
		return 0, err
	}
	if value == "" {
		return 0, fmt.Errorf("powercap: %s is empty", path)
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("powercap: invalid value %q at %s", value, path)
	}
	return parsed, nil
}

func isTopLevelZone(name string) bool {
	if strings.HasPrefix(name, "intel-rapl-mmio:") {
		return strings.Count(name, ":") == 1
	}
	return strings.HasPrefix(name, "intel-rapl:") && strings.Count(name, ":") == 1
}

func sourceRank(zone string) int {
	if strings.HasPrefix(zone, "intel-rapl:") {
		return 0
	}
	return 1
}
