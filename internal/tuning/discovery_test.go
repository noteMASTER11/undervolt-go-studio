package tuning

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestDiscoveryPublishesFastDriverBeforeSlowDriver(t *testing.T) {
	fast := &delayedDriver{id: "intel.powercap", capabilities: []Capability{{ID: ControlPL1, State: StateSupported}}}
	slow := &delayedDriver{id: "intel.msr", delay: 100 * time.Millisecond, capabilities: []Capability{{ID: ControlRatioPCore, State: StateSupported}}}
	results := NewDiscoverer("machine", fast, slow).Discover(context.Background())

	first := <-results
	if !hasCapability(first.Set, ControlPL1) || hasCapability(first.Set, ControlRatioPCore) {
		t.Fatalf("first = %+v", first)
	}
	second := <-results
	if !second.Complete || !hasCapability(second.Set, ControlPL1) || !hasCapability(second.Set, ControlRatioPCore) {
		t.Fatalf("second = %+v", second)
	}
}

func TestDiscoveryStopsAfterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for result := range NewDiscoverer("machine", &delayedDriver{id: "blocked", delay: time.Hour}).Discover(ctx) {
		t.Fatalf("published after cancel: %+v", result)
	}
}

func TestDiscoveryPrefersSupportedKernelCapabilityOverMSR(t *testing.T) {
	kernel := &delayedDriver{id: "intel.powercap", capabilities: []Capability{{ID: ControlPL1, State: StateSupported, DriverID: "intel.powercap", Current: NumericValue(44)}}}
	fallback := &delayedDriver{id: "intel.msr", capabilities: []Capability{{ID: ControlPL1, State: StateSupported, DriverID: "intel.msr", Current: NumericValue(40)}}}

	var final DiscoveryResult
	for result := range NewDiscoverer("machine", kernel, fallback).Discover(context.Background()) {
		final = result
	}
	capability := capabilityFromSet(t, final.Set, ControlPL1)
	if capability.DriverID != "intel.powercap" || capability.Current.Number != 44 {
		t.Fatalf("selected capability = %+v", capability)
	}
}

func TestDiscoveryUsesSupportedMSRWhenKernelControlIsNotWritable(t *testing.T) {
	kernel := &delayedDriver{id: "intel.powercap", capabilities: []Capability{{ID: ControlPL2, State: StateReadOnly, DriverID: "intel.powercap"}}}
	fallback := &delayedDriver{id: "intel.msr", capabilities: []Capability{{ID: ControlPL2, State: StateSupported, DriverID: "intel.msr"}}}

	var final DiscoveryResult
	for result := range NewDiscoverer("machine", kernel, fallback).Discover(context.Background()) {
		final = result
	}
	if got := capabilityFromSet(t, final.Set, ControlPL2).DriverID; got != "intel.msr" {
		t.Fatalf("selected driver = %q", got)
	}
}

func TestDiscoveryGenerationIsStableAcrossCompletionOrderAndObservedTime(t *testing.T) {
	a := &delayedDriver{id: "a", capabilities: []Capability{{ID: ControlPL1, State: StateSupported, DriverID: "a", Current: NumericValue(40), ObservedAt: time.Now()}}}
	b := &delayedDriver{id: "b", capabilities: []Capability{{ID: ControlEPP, State: StateSupported, DriverID: "b", Current: ChoiceValue("default"), ObservedAt: time.Now()}}}
	first := finalDiscovery(t, NewDiscoverer("machine", a, b))

	a.delay, b.delay = 20*time.Millisecond, 0
	a.capabilities[0].ObservedAt = time.Now().Add(time.Hour)
	b.capabilities[0].ObservedAt = time.Now().Add(time.Hour)
	second := finalDiscovery(t, NewDiscoverer("machine", a, b))
	if first.Set.Generation != second.Set.Generation {
		t.Fatalf("generation changed: %q != %q", first.Set.Generation, second.Set.Generation)
	}
}

func TestNewDiscoverySuppressesLateResultsFromPreviousRun(t *testing.T) {
	discoverer := NewDiscoverer("machine", &delayedDriver{id: "slow", delay: 50 * time.Millisecond, capabilities: []Capability{{ID: ControlPL1}}})
	oldResults := discoverer.Discover(context.Background())
	newResults := discoverer.Discover(context.Background())

	for range newResults {
	}
	select {
	case result, ok := <-oldResults:
		if ok {
			t.Fatalf("old run published late result: %+v", result)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("old discovery channel did not close")
	}
}

type delayedDriver struct {
	id           string
	delay        time.Duration
	capabilities []Capability
	err          error
}

func (driver *delayedDriver) ID() string { return driver.id }

func (driver *delayedDriver) Probe(ctx context.Context) ([]Capability, error) {
	if driver.delay > 0 {
		timer := time.NewTimer(driver.delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return append([]Capability(nil), driver.capabilities...), driver.err
}

func (driver *delayedDriver) Prepare(context.Context, Change) (PreparedOperation, error) {
	return nil, errors.New("not implemented")
}

func (driver *delayedDriver) Restore(context.Context, ControlID, json.RawMessage) (Value, error) {
	return Value{}, errors.New("not implemented")
}

func hasCapability(set CapabilitySet, id ControlID) bool {
	for _, capability := range set.Capabilities {
		if capability.ID == id {
			return true
		}
	}
	return false
}

func capabilityFromSet(t *testing.T, set CapabilitySet, id ControlID) Capability {
	t.Helper()
	for _, capability := range set.Capabilities {
		if capability.ID == id {
			return capability
		}
	}
	t.Fatalf("capability %s not found", id)
	return Capability{}
}

func finalDiscovery(t *testing.T, discoverer *Discoverer) DiscoveryResult {
	t.Helper()
	var final DiscoveryResult
	for result := range discoverer.Discover(context.Background()) {
		final = result
	}
	if !final.Complete {
		t.Fatalf("final result = %+v", final)
	}
	return final
}
