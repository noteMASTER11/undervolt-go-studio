package viewmodel

import (
	"context"
	"errors"
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

func TestTuneApplyRequiresReviewAndRejectsConcurrentConfirmation(t *testing.T) {
	service := &controlledTuneService{applyStarted: make(chan struct{}), applyRelease: make(chan struct{})}
	viewModel := NewTune(service)
	activateTuneWithCapabilities(t, viewModel, service)
	if err := viewModel.Stage(tuning.ControlPL1, tuning.NumericValue(44)); err != nil {
		t.Fatal(err)
	}
	if err := viewModel.Apply(context.Background()); !errors.Is(err, ErrReviewRequired) {
		t.Fatalf("apply without review error = %v", err)
	}
	if _, err := viewModel.Review(); err != nil {
		t.Fatal(err)
	}
	first := make(chan error, 1)
	go func() { first <- viewModel.Apply(context.Background()) }()
	select {
	case <-service.applyStarted:
	case <-time.After(time.Second):
		t.Fatal("first apply did not start")
	}
	if err := viewModel.Apply(context.Background()); !errors.Is(err, ErrApplyInProgress) {
		t.Fatalf("second apply error = %v", err)
	}
	close(service.applyRelease)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	viewModel.Deactivate()
}

func TestTuneKeepsSessionActiveAndRejectsEditsUntilRevert(t *testing.T) {
	events := make(chan tuning.Event, 1)
	service := &controlledTuneService{events: events}
	viewModel := NewTune(service)
	activateTuneWithCapabilities(t, viewModel, service)
	if err := viewModel.Stage(tuning.ControlPL1, tuning.NumericValue(44)); err != nil {
		t.Fatal(err)
	}
	if _, err := viewModel.Review(); err != nil {
		t.Fatal(err)
	}
	if err := viewModel.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	events <- tuning.Event{Kind: "transaction_applied"}
	close(events)
	waitForTunePhase(t, viewModel, PhaseActive)
	if !viewModel.State().SessionActive {
		t.Fatal("applied transaction was not retained as active")
	}
	if err := viewModel.Stage(tuning.ControlPL1, tuning.NumericValue(42)); !errors.Is(err, ErrSessionActive) {
		t.Fatalf("stage during active session error = %v", err)
	}
	if viewModel.State().Phase != PhaseActive {
		t.Fatalf("phase = %q, want active", viewModel.State().Phase)
	}
	viewModel.Deactivate()
}

func TestTuneCancelReviewReturnsToStagedWithoutLosingChanges(t *testing.T) {
	service := &controlledTuneService{}
	viewModel := NewTune(service)
	activateTuneWithCapabilities(t, viewModel, service)
	if err := viewModel.Stage(tuning.ControlPL1, tuning.NumericValue(44)); err != nil {
		t.Fatal(err)
	}
	if _, err := viewModel.Review(); err != nil {
		t.Fatal(err)
	}
	viewModel.CancelReview()
	state := viewModel.State()
	if state.Phase != PhaseStaged || len(state.Pending) != 1 || len(state.Review) != 0 {
		t.Fatalf("state after cancel = %+v", state)
	}
	viewModel.Deactivate()
}

func TestTuneReviewChangedRequiresFreshReview(t *testing.T) {
	events := make(chan tuning.Event, 1)
	service := &controlledTuneService{events: events}
	viewModel := NewTune(service)
	activateTuneWithCapabilities(t, viewModel, service)
	if err := viewModel.Stage(tuning.ControlPL1, tuning.NumericValue(44)); err != nil {
		t.Fatal(err)
	}
	if _, err := viewModel.Review(); err != nil {
		t.Fatal(err)
	}
	if err := viewModel.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	updated := capabilitySet("privileged")
	events <- tuning.Event{Kind: "review_changed", Capabilities: &updated}
	close(events)
	waitForTunePhase(t, viewModel, PhaseStaged)
	state := viewModel.State()
	if len(state.Review) != 0 || len(state.Pending) != 1 {
		t.Fatalf("state after capability change = %+v", state)
	}
	if err := viewModel.Apply(context.Background()); !errors.Is(err, ErrReviewRequired) {
		t.Fatalf("apply without fresh review error = %v", err)
	}
	viewModel.Deactivate()
}

func TestTuneMarksInvalidPendingSetAsNotReviewable(t *testing.T) {
	service := &controlledTuneService{}
	viewModel := NewTune(service)
	activateTuneWithCapabilities(t, viewModel, service)
	if err := viewModel.Stage(tuning.ControlPL1, tuning.ChoiceValue("invalid")); err == nil {
		t.Fatal("invalid staged value was accepted")
	}
	state := viewModel.State()
	if state.PendingValid {
		t.Fatal("invalid pending set was marked reviewable")
	}
	if _, err := viewModel.Review(); err == nil {
		t.Fatal("invalid pending set was reviewed")
	}
	viewModel.Deactivate()
}

type controlledTuneService struct {
	mu           sync.Mutex
	discovery    []chan tuning.DiscoveryResult
	applyCount   int
	applyErr     error
	applyStarted chan struct{}
	applyRelease chan struct{}
	events       chan tuning.Event
	revertCount  int
	revertErr    error
	closeErr     error
}

func (service *controlledTuneService) Discover(context.Context) <-chan tuning.DiscoveryResult {
	service.mu.Lock()
	defer service.mu.Unlock()
	request := make(chan tuning.DiscoveryResult, 2)
	service.discovery = append(service.discovery, request)
	return request
}

func (service *controlledTuneService) Apply(ctx context.Context, _ tuning.ChangeSet) (<-chan tuning.Event, error) {
	service.mu.Lock()
	service.applyCount++
	service.mu.Unlock()
	if service.applyStarted != nil {
		close(service.applyStarted)
	}
	if service.applyRelease != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-service.applyRelease:
		}
	}
	if service.applyErr != nil {
		return nil, service.applyErr
	}
	if service.events != nil {
		return service.events, nil
	}
	events := make(chan tuning.Event)
	close(events)
	return events, nil
}

func (service *controlledTuneService) Revert(context.Context) error {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.revertCount++
	return service.revertErr
}
func (service *controlledTuneService) Close(context.Context) error { return service.closeErr }

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

func activateTuneWithCapabilities(t *testing.T, viewModel *Tune, service *controlledTuneService) {
	t.Helper()
	viewModel.Activate()
	request := service.lastRequest()
	request <- tuning.DiscoveryResult{Set: capabilitySet("g"), Complete: true}
	close(request)
	waitForTunePhase(t, viewModel, PhaseIdle)
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

func TestTunePersistsRollbackDetailsAcrossNavigationAndReset(t *testing.T) {
	vm := NewTune(&controlledTuneService{})
	stream := make(chan tuning.Event, 3)
	stream <- tuning.Event{Kind: "transaction_applied", Effective: map[tuning.ControlID]tuning.Value{tuning.ControlPL1: tuning.NumericValue(39.5)}}
	stream <- tuning.Event{Kind: "rollback_incomplete", Message: "Stock restoration incomplete", Remaining: map[tuning.ControlID]tuning.Value{tuning.ControlPL1: tuning.NumericValue(39.5)}}
	stream <- tuning.Event{Kind: "session_terminated", Message: "Helper lost"}
	close(stream)
	vm.consumeEvents(stream)
	vm.Reset()
	vm.Activate()
	vm.Deactivate()
	state := vm.State()
	if state.Phase != PhaseRollbackIncomplete || state.Remaining[tuning.ControlPL1].Number != 39.5 || state.LastError == "" {
		t.Fatalf("critical outcome lost: %+v", state)
	}
	if err := vm.Stage(tuning.ControlPL1, tuning.NumericValue(40)); err == nil {
		t.Fatal("staging allowed before recovery")
	}
}

func TestTuneLeaseRollbackClearsActiveSession(t *testing.T) {
	vm := NewTune(&controlledTuneService{})
	stream := make(chan tuning.Event, 2)
	stream <- tuning.Event{Kind: "transaction_applied"}
	stream <- tuning.Event{Kind: "rollback_complete"}
	close(stream)
	vm.consumeEvents(stream)
	if state := vm.State(); state.SessionActive || state.Phase != PhaseIdle {
		t.Fatalf("lease rollback left UI active: %+v", state)
	}
}

func TestTuneKeepsStartupRecoveryFailureUntilRetryIsVerified(t *testing.T) {
	startupErr := &tuning.RollbackError{Cause: errors.New("startup recovery failed")}
	retryErr := &tuning.RollbackError{Cause: errors.New("retry recovery failed")}
	service := &controlledTuneService{applyErr: startupErr, revertErr: retryErr, closeErr: retryErr}
	viewModel := NewTune(service)
	activateTuneWithCapabilities(t, viewModel, service)
	if err := viewModel.Stage(tuning.ControlPL1, tuning.NumericValue(44)); err != nil {
		t.Fatal(err)
	}
	if _, err := viewModel.Review(); err != nil {
		t.Fatal(err)
	}
	if err := viewModel.Apply(context.Background()); !errors.Is(err, startupErr) {
		t.Fatalf("apply error = %v, want startup recovery failure", err)
	}
	if err := viewModel.Revert(context.Background()); !errors.Is(err, retryErr) {
		t.Fatalf("retry error = %v, want recovery failure", err)
	}
	state := viewModel.State()
	if state.Phase != PhaseRollbackIncomplete || !state.SessionActive || state.LastError == "" {
		t.Fatalf("recovery warning cleared after failed retry: %+v", state)
	}
	service.mu.Lock()
	revertCount := service.revertCount
	service.mu.Unlock()
	if revertCount != 1 {
		t.Fatalf("revert calls = %d, want 1", revertCount)
	}
	if err := viewModel.Close(); !errors.Is(err, retryErr) {
		t.Fatalf("close error = %v, want recovery failure", err)
	}
	if state := viewModel.State(); state.Phase != PhaseRollbackIncomplete || !state.SessionActive {
		t.Fatalf("recovery warning cleared after failed close: %+v", state)
	}
}
