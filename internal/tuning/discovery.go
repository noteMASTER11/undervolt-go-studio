package tuning

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"sync/atomic"
	"time"
)

type DiscoveryResult struct {
	Set      CapabilitySet
	Complete bool
	Err      error
}

type Discoverer struct {
	MachineID     string
	Drivers       []Driver
	DriverTimeout time.Duration

	run atomic.Uint64
}

func NewDiscoverer(machineID string, drivers ...Driver) *Discoverer {
	return &Discoverer{
		MachineID:     machineID,
		Drivers:       append([]Driver(nil), drivers...),
		DriverTimeout: 2 * time.Second,
	}
}

func (discoverer *Discoverer) Discover(ctx context.Context) <-chan DiscoveryResult {
	results := make(chan DiscoveryResult, max(1, len(discoverer.Drivers)))
	run := discoverer.run.Add(1)
	go discoverer.runDiscovery(ctx, run, results)
	return results
}

type driverProbeResult struct {
	index        int
	driverID     string
	capabilities []Capability
	err          error
}

func (discoverer *Discoverer) runDiscovery(ctx context.Context, run uint64, output chan<- DiscoveryResult) {
	defer close(output)
	if ctx.Err() != nil || discoverer.run.Load() != run {
		return
	}
	if len(discoverer.Drivers) == 0 {
		set := CapabilitySet{MachineID: discoverer.MachineID}
		set.Generation = generationFor(set)
		output <- DiscoveryResult{Set: set, Complete: true}
		return
	}

	completed := make(chan driverProbeResult, len(discoverer.Drivers))
	for index, driver := range discoverer.Drivers {
		go discoverer.probeDriver(ctx, index, driver, completed)
	}

	merged := make(map[ControlID]Capability)
	var accumulatedErr error
	for count := 1; count <= len(discoverer.Drivers); count++ {
		var result driverProbeResult
		select {
		case <-ctx.Done():
			return
		case result = <-completed:
		}
		if discoverer.run.Load() != run {
			return
		}
		if result.err != nil {
			accumulatedErr = errors.Join(accumulatedErr, result.err)
		}
		for _, capability := range result.capabilities {
			if capability.DriverID == "" {
				capability.DriverID = result.driverID
			}
			if existing, ok := merged[capability.ID]; !ok || preferCapability(capability, existing) {
				merged[capability.ID] = cloneCapability(capability)
			}
		}
		set := immutableSet(discoverer.MachineID, merged)
		publication := DiscoveryResult{Set: set, Complete: count == len(discoverer.Drivers), Err: accumulatedErr}
		select {
		case <-ctx.Done():
			return
		case output <- publication:
		}
	}
}

func (discoverer *Discoverer) probeDriver(ctx context.Context, index int, driver Driver, output chan<- driverProbeResult) {
	probeContext := ctx
	cancel := func() {}
	if discoverer.DriverTimeout > 0 {
		probeContext, cancel = context.WithTimeout(ctx, discoverer.DriverTimeout)
	}
	defer cancel()
	capabilities, err := driver.Probe(probeContext)
	output <- driverProbeResult{
		index:        index,
		driverID:     driver.ID(),
		capabilities: capabilities,
		err:          err,
	}
}

func preferCapability(candidate, current Capability) bool {
	candidateKernel := kernelDriver(candidate.DriverID)
	currentKernel := kernelDriver(current.DriverID)
	if candidateKernel && candidate.State == StateSupported {
		return !(currentKernel && current.State == StateSupported && driverPriority(current.DriverID) <= driverPriority(candidate.DriverID))
	}
	if currentKernel && current.State == StateSupported {
		return false
	}
	if candidate.State == StateSupported && current.State != StateSupported {
		return true
	}
	if current.State == StateSupported && candidate.State != StateSupported {
		return false
	}
	return driverPriority(candidate.DriverID) < driverPriority(current.DriverID)
}

func kernelDriver(id string) bool {
	return id == "intel.powercap" || id == "intel.epp" || id == "intel.tcc"
}

func driverPriority(id string) int {
	switch id {
	case "intel.powercap":
		return 0
	case "intel.epp":
		return 1
	case "intel.tcc":
		return 2
	case "intel.msr":
		return 10
	default:
		return 20
	}
}

func immutableSet(machineID string, merged map[ControlID]Capability) CapabilitySet {
	capabilities := make([]Capability, 0, len(merged))
	for _, capability := range merged {
		capabilities = append(capabilities, cloneCapability(capability))
	}
	sort.Slice(capabilities, func(i, j int) bool { return capabilities[i].ID < capabilities[j].ID })
	set := CapabilitySet{MachineID: machineID, Capabilities: capabilities}
	set.Generation = generationFor(set)
	return set
}

func cloneCapability(capability Capability) Capability {
	cloned := capability
	if capability.Range != nil {
		value := *capability.Range
		cloned.Range = &value
	}
	cloned.Choices = append([]string(nil), capability.Choices...)
	cloned.Current.Vector = append([]float64(nil), capability.Current.Vector...)
	return cloned
}

type generationCapability struct {
	SourceRevision string
	ID             ControlID
	State          CapabilityState
	Unit           Unit
	Current        Value
	Range          *NumericRange
	Choices        []string
	DriverID       string
	ReasonCode     string
	Experimental   bool
}

func generationFor(set CapabilitySet) string {
	signature := struct {
		MachineID    string
		Capabilities []generationCapability
	}{MachineID: set.MachineID, Capabilities: make([]generationCapability, 0, len(set.Capabilities))}
	for _, capability := range set.Capabilities {
		signature.Capabilities = append(signature.Capabilities, generationCapability{
			SourceRevision: capability.SourceRevision,
			ID:             capability.ID,
			State:          capability.State,
			Unit:           capability.Unit,
			Current:        capability.Current,
			Range:          capability.Range,
			Choices:        capability.Choices,
			DriverID:       capability.DriverID,
			ReasonCode:     capability.ReasonCode,
			Experimental:   capability.Experimental,
		})
	}
	encoded, _ := json.Marshal(signature)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:8])
}
