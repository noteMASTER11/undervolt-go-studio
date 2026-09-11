package viewmodel

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
)

func TestTuneConstructionStartsNoDiscoveryAndActivationIsIdempotent(t *testing.T) {
	service := &controlledTuneService{}
	viewModel := NewTune(service)
	if service.requestCount() != 0 {
		t.Fatal("construction started discovery")
	}
	viewModel.Activate()
	viewModel.Activate()
	if service.requestCount() != 1 {
		t.Fatalf("discovery requests = %d", service.requestCount())
	}
	viewModel.Deactivate()
}

func TestTuneIgnoresDiscoveryFromPreviousActivation(t *testing.T) {
	service := &controlledTuneService{}
	viewModel := NewTune(service)
	viewModel.Activate()
	first := service.lastRequest()
	viewModel.Deactivate()
	viewModel.Activate()
	first <- tuning.DiscoveryResult{Set: capabilitySet("old"), Complete: true}
	time.Sleep(20 * time.Millisecond)
	if viewModel.State().Capabilities.Generation == "old" {
		t.Fatal("stale discovery replaced state")
	}
	viewModel.Deactivate()
}

func TestTuneStagesAndReviewsNormalizedValueWithoutApplying(t *testing.T) {
	service := &controlledTuneService{}
	viewModel := NewTune(service)
	viewModel.Activate()
	request := service.lastRequest()
	request <- tuning.DiscoveryResult{Set: capabilitySet("g"), Complete: true}
	close(request)
	waitForTunePhase(t, viewModel, PhaseIdle)
	if err := viewModel.Stage(tuning.ControlPL1, tuning.NumericValue(44.06)); err != nil {
		t.Fatal(err)
	}
	if service.applyCount != 0 {
		t.Fatal("staging changed hardware")
	}
	review, err := viewModel.Review()
	if err != nil {
		t.Fatal(err)
	}
	if len(review) != 1 || review[0].Normalized.Number != 44 || !review[0].Adjusted {
		t.Fatalf("review = %+v", review)
	}
	viewModel.Reset()
	if len(viewModel.State().Pending) != 0 {
		t.Fatal("reset retained pending changes")
	}
	viewModel.Deactivate()
}

type controlledTuneService struct {
	mu         sync.Mutex
	discovery  []chan tuning.DiscoveryResult
	applyCount int
}

func (service *controlledTuneService) Discover(context.Context) <-chan tuning.DiscoveryResult {
	service.mu.Lock()
	defer service.mu.Unlock()
	request := make(chan tuning.DiscoveryResult, 2)
	service.discovery = append(service.discovery, request)
	return request
}

func (service *controlledTuneService) Apply(context.Context, tuning.ChangeSet) (<-chan tuning.Event, error) {
	service.mu.Lock()
	service.applyCount++
	service.mu.Unlock()
	events := make(chan tuning.Event)
	close(events)
	return events, nil
}

func (service *controlledTuneService) Revert(context.Context) error { return nil }
func (service *controlledTuneService) Close(context.Context) error  { return nil }

func (service *controlledTuneService) requestCount() int {
	service.mu.Lock()
	defer service.mu.Unlock()
	return len(service.discovery)
}

func (service *controlledTuneService) lastRequest() chan tuning.DiscoveryResult {
	service.mu.Lock()
	defer service.mu.Unlock()
	return service.discovery[len(service.discovery)-1]
}

func capabilitySet(generation string) tuning.CapabilitySet {
	return tuning.CapabilitySet{Generation: generation, MachineID: "cpu", Capabilities: []tuning.Capability{{
		ID: tuning.ControlPL1, Label: "Sustained power", Unit: tuning.UnitWatt, State: tuning.StateSupported,
		Current: tuning.NumericValue(40), Range: &tuning.NumericRange{Minimum: 15, Maximum: 55, Step: 0.125},
	}}}
}

func waitForTunePhase(t *testing.T, viewModel *Tune, phase string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if viewModel.State().Phase == phase {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("phase = %q, want %q", viewModel.State().Phase, phase)
}
