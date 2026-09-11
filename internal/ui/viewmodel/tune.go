package viewmodel

import (
	"context"
	"errors"
	"sync"
	"time"

	privilegeclient "github.com/noteMASTER11/undervolt-go-studio/internal/privilege/client"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
)

const (
	PhaseIdle               = "idle"
	PhaseDiscovering        = "discovering"
	PhaseStaged             = "staged"
	PhaseReviewing          = "reviewing"
	PhaseAuthorizing        = "authorizing"
	PhaseApplying           = "applying"
	PhaseActive             = "active"
	PhaseRollingBack        = "rolling_back"
	PhaseFailed             = "failed"
	PhaseRollbackIncomplete = "rollback_incomplete"
)

type TuneService interface {
	Discover(context.Context) <-chan tuning.DiscoveryResult
	Apply(context.Context, tuning.ChangeSet) (<-chan tuning.Event, error)
	Revert(context.Context) error
	Close(context.Context) error
}

type TuneState struct {
	Active       bool
	Phase        string
	Capabilities tuning.CapabilitySet
	Pending      []tuning.Change
	Review       []ReviewRow
	Effective    map[tuning.ControlID]tuning.Value
	LastError    string
}

type ReviewRow struct {
	ID         tuning.ControlID
	Label      string
	Stock      tuning.Value
	Requested  tuning.Value
	Normalized tuning.Value
	Adjusted   bool
}

type Tune struct {
	service TuneService

	mu         sync.RWMutex
	state      TuneState
	listener   StateListener[TuneState]
	activation uint64
	cancel     context.CancelFunc
	closeOnce  sync.Once
	closeErr   error
}

func NewTune(service TuneService) *Tune {
	return &Tune{service: service, state: TuneState{Phase: PhaseIdle, Effective: make(map[tuning.ControlID]tuning.Value)}}
}

func (viewModel *Tune) Activate() {
	viewModel.mu.Lock()
	if viewModel.state.Active {
		viewModel.mu.Unlock()
		return
	}
	viewModel.state.Active = true
	viewModel.activation++
	token := viewModel.activation
	ctx, cancel := context.WithCancel(context.Background())
	viewModel.cancel = cancel
	if viewModel.state.Phase != PhaseActive {
		viewModel.state.Phase = PhaseDiscovering
	}
	state, listener := viewModel.stateLocked(), viewModel.listener
	viewModel.mu.Unlock()
	notify(listener, state)
	results := viewModel.service.Discover(ctx)
	go viewModel.consumeDiscovery(ctx, token, results)
}

func (viewModel *Tune) Deactivate() {
	viewModel.mu.Lock()
	if !viewModel.state.Active {
		viewModel.mu.Unlock()
		return
	}
	viewModel.state.Active = false
	viewModel.activation++
	cancel := viewModel.cancel
	viewModel.cancel = nil
	if viewModel.state.Phase == PhaseDiscovering {
		viewModel.state.Phase = PhaseIdle
	}
	state, listener := viewModel.stateLocked(), viewModel.listener
	viewModel.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	notify(listener, state)
}

func (viewModel *Tune) Stage(id tuning.ControlID, value tuning.Value) error {
	viewModel.mu.Lock()
	pending := append([]tuning.Change(nil), viewModel.state.Pending...)
	found := false
	for index := range pending {
		if pending[index].ID == id {
			pending[index].Requested = cloneValue(value)
			found = true
			break
		}
	}
	if !found {
		pending = append(pending, tuning.Change{ID: id, Requested: cloneValue(value)})
	}
	set := tuning.ChangeSet{Generation: viewModel.state.Capabilities.Generation, MachineID: viewModel.state.Capabilities.MachineID, Changes: pending}
	_, err := tuning.ValidateChangeSet(viewModel.state.Capabilities, set)
	viewModel.state.Pending = pending
	viewModel.state.Review = nil
	viewModel.state.Phase = PhaseStaged
	viewModel.state.LastError = ""
	if err != nil {
		viewModel.state.LastError = err.Error()
	}
	state, listener := viewModel.stateLocked(), viewModel.listener
	viewModel.mu.Unlock()
	notify(listener, state)
	return err
}

func (viewModel *Tune) Reset() {
	viewModel.mu.Lock()
	viewModel.state.Pending = nil
	viewModel.state.Review = nil
	viewModel.state.LastError = ""
	if viewModel.state.Phase != PhaseActive {
		viewModel.state.Phase = PhaseIdle
	}
	state, listener := viewModel.stateLocked(), viewModel.listener
	viewModel.mu.Unlock()
	notify(listener, state)
}

func (viewModel *Tune) Review() ([]ReviewRow, error) {
	viewModel.mu.Lock()
	set := tuning.ChangeSet{
		Generation: viewModel.state.Capabilities.Generation,
		MachineID:  viewModel.state.Capabilities.MachineID,
		Changes:    append([]tuning.Change(nil), viewModel.state.Pending...),
	}
	validation, err := tuning.ValidateChangeSet(viewModel.state.Capabilities, set)
	if err != nil {
		viewModel.state.LastError = err.Error()
		state, listener := viewModel.stateLocked(), viewModel.listener
		viewModel.mu.Unlock()
		notify(listener, state)
		return nil, err
	}
	byID := make(map[tuning.ControlID]tuning.Capability, len(viewModel.state.Capabilities.Capabilities))
	for _, capability := range viewModel.state.Capabilities.Capabilities {
		byID[capability.ID] = capability
	}
	adjusted := make(map[tuning.ControlID]bool, len(validation.Adjustments))
	for _, adjustment := range validation.Adjustments {
		adjusted[adjustment.ID] = true
	}
	rows := make([]ReviewRow, 0, len(validation.ChangeSet.Changes))
	for index, change := range validation.ChangeSet.Changes {
		capability := byID[change.ID]
		rows = append(rows, ReviewRow{
			ID: change.ID, Label: capability.Label, Stock: cloneValue(capability.Current),
			Requested: cloneValue(set.Changes[index].Requested), Normalized: cloneValue(change.Requested), Adjusted: adjusted[change.ID],
		})
	}
	viewModel.state.Review = rows
	viewModel.state.Phase = PhaseReviewing
	viewModel.state.LastError = ""
	state, listener := viewModel.stateLocked(), viewModel.listener
	viewModel.mu.Unlock()
	notify(listener, state)
	return append([]ReviewRow(nil), rows...), nil
}

func (viewModel *Tune) Apply(ctx context.Context) error {
	viewModel.mu.Lock()
	set := tuning.ChangeSet{Generation: viewModel.state.Capabilities.Generation, MachineID: viewModel.state.Capabilities.MachineID, Changes: append([]tuning.Change(nil), viewModel.state.Pending...)}
	viewModel.state.Phase = PhaseAuthorizing
	state, listener := viewModel.stateLocked(), viewModel.listener
	viewModel.mu.Unlock()
	notify(listener, state)
	events, err := viewModel.service.Apply(ctx, set)
	if err != nil {
		viewModel.mu.Lock()
		viewModel.state.Phase = PhaseFailed
		viewModel.state.LastError = err.Error()
		if errors.Is(err, privilegeclient.ErrAuthorizationCancelled) {
			viewModel.state.Phase = PhaseStaged
			viewModel.state.LastError = "Authorization cancelled; nothing was changed."
		}
		state, listener = viewModel.stateLocked(), viewModel.listener
		viewModel.mu.Unlock()
		notify(listener, state)
		return err
	}
	viewModel.mu.Lock()
	viewModel.state.Phase = PhaseApplying
	state, listener = viewModel.stateLocked(), viewModel.listener
	viewModel.mu.Unlock()
	notify(listener, state)
	go viewModel.consumeEvents(events)
	return nil
}

func (viewModel *Tune) Revert(ctx context.Context) error {
	viewModel.setPhase(PhaseRollingBack, "")
	if err := viewModel.service.Revert(ctx); err != nil {
		viewModel.setPhase(PhaseRollbackIncomplete, err.Error())
		return err
	}
	viewModel.mu.Lock()
	viewModel.state.Pending = nil
	viewModel.state.Review = nil
	viewModel.state.Effective = make(map[tuning.ControlID]tuning.Value)
	viewModel.mu.Unlock()
	viewModel.setPhase(PhaseIdle, "")
	return nil
}

func (viewModel *Tune) Close() error {
	viewModel.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		viewModel.closeErr = viewModel.service.Close(ctx)
		viewModel.Deactivate()
	})
	return viewModel.closeErr
}

func (viewModel *Tune) SetListener(listener StateListener[TuneState]) {
	viewModel.mu.Lock()
	viewModel.listener = listener
	state := viewModel.stateLocked()
	viewModel.mu.Unlock()
	notify(listener, state)
}

func (viewModel *Tune) State() TuneState {
	viewModel.mu.RLock()
	defer viewModel.mu.RUnlock()
	return viewModel.stateLocked()
}

func (viewModel *Tune) consumeDiscovery(ctx context.Context, token uint64, results <-chan tuning.DiscoveryResult) {
	for {
		select {
		case <-ctx.Done():
			return
		case result, ok := <-results:
			if !ok {
				return
			}
			viewModel.mu.Lock()
			if token != viewModel.activation || !viewModel.state.Active {
				viewModel.mu.Unlock()
				continue
			}
			viewModel.state.Capabilities = cloneCapabilitySet(result.Set)
			if result.Err != nil {
				viewModel.state.LastError = result.Err.Error()
			}
			if result.Complete {
				viewModel.state.Phase = PhaseIdle
				if len(viewModel.state.Pending) > 0 {
					viewModel.state.Phase = PhaseStaged
				}
			}
			state, listener := viewModel.stateLocked(), viewModel.listener
			viewModel.mu.Unlock()
			notify(listener, state)
		}
	}
}

func (viewModel *Tune) consumeEvents(events <-chan tuning.Event) {
	for event := range events {
		viewModel.mu.Lock()
		for id, value := range event.Effective {
			viewModel.state.Effective[id] = cloneValue(value)
		}
		switch event.Kind {
		case "review_changed":
			if event.Capabilities != nil {
				viewModel.state.Capabilities = cloneCapabilitySet(*event.Capabilities)
			}
			viewModel.state.Phase = PhaseReviewing
		case "transaction_applied", "applied":
			viewModel.state.Phase = PhaseActive
		case "rollback_incomplete":
			viewModel.state.Phase = PhaseRollbackIncomplete
		case "transaction_failed", "failed":
			viewModel.state.Phase = PhaseFailed
			viewModel.state.LastError = event.Message
		default:
			viewModel.state.Phase = PhaseApplying
		}
		state, listener := viewModel.stateLocked(), viewModel.listener
		viewModel.mu.Unlock()
		notify(listener, state)
	}
}

func (viewModel *Tune) setPhase(phase, lastError string) {
	viewModel.mu.Lock()
	viewModel.state.Phase = phase
	viewModel.state.LastError = lastError
	state, listener := viewModel.stateLocked(), viewModel.listener
	viewModel.mu.Unlock()
	notify(listener, state)
}

func (viewModel *Tune) stateLocked() TuneState {
	state := viewModel.state
	state.Capabilities = cloneCapabilitySet(viewModel.state.Capabilities)
	state.Pending = append([]tuning.Change(nil), viewModel.state.Pending...)
	state.Review = append([]ReviewRow(nil), viewModel.state.Review...)
	state.Effective = make(map[tuning.ControlID]tuning.Value, len(viewModel.state.Effective))
	for id, value := range viewModel.state.Effective {
		state.Effective[id] = cloneValue(value)
	}
	return state
}

func cloneCapabilitySet(set tuning.CapabilitySet) tuning.CapabilitySet {
	cloned := set
	cloned.Capabilities = append([]tuning.Capability(nil), set.Capabilities...)
	for index := range cloned.Capabilities {
		cloned.Capabilities[index].Choices = append([]string(nil), set.Capabilities[index].Choices...)
		cloned.Capabilities[index].Current = cloneValue(set.Capabilities[index].Current)
		if set.Capabilities[index].Range != nil {
			rangeCopy := *set.Capabilities[index].Range
			cloned.Capabilities[index].Range = &rangeCopy
		}
	}
	return cloned
}

func cloneValue(value tuning.Value) tuning.Value {
	value.Vector = append([]float64(nil), value.Vector...)
	return value
}

func notify(listener StateListener[TuneState], state TuneState) {
	if listener != nil {
		listener(state)
	}
}
