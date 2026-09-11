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

var (
	ErrReviewRequired  = errors.New("tuning changes must be reviewed before apply")
	ErrApplyInProgress = errors.New("a tuning apply is already in progress")
	ErrSessionActive   = errors.New("revert the active tuning session before editing")
)

type TuneService interface {
	Discover(context.Context) <-chan tuning.DiscoveryResult
	Apply(context.Context, tuning.ChangeSet) (<-chan tuning.Event, error)
	Revert(context.Context) error
	Close(context.Context) error
}

type TuneState struct {
	Active        bool
	SessionActive bool
	PendingValid  bool
	Phase         string
	Capabilities  tuning.CapabilitySet
	Pending       []tuning.Change
	Review        []ReviewRow
	Effective     map[tuning.ControlID]tuning.Value
	Remaining     map[tuning.ControlID]tuning.Value
	Unverified    []tuning.ControlID
	LastError     string
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

	mu            sync.RWMutex
	state         TuneState
	listener      StateListener[TuneState]
	activation    uint64
	cancel        context.CancelFunc
	closeMu       sync.Mutex
	closed        bool
	sessionStream bool
}

func NewTune(service TuneService) *Tune {
	vm := &Tune{service: service, state: TuneState{Phase: PhaseIdle, Effective: make(map[tuning.ControlID]tuning.Value)}}
	if source, ok := service.(interface{ Events() <-chan tuning.Event }); ok {
		vm.sessionStream = true
		go vm.consumeEvents(source.Events())
	}
	return vm
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
	if !viewModel.state.SessionActive && viewModel.state.Phase != PhaseRollbackIncomplete {
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
	if viewModel.state.SessionActive {
		viewModel.mu.Unlock()
		return ErrSessionActive
	}
	if viewModel.state.Phase == PhaseAuthorizing || viewModel.state.Phase == PhaseApplying || viewModel.state.Phase == PhaseRollingBack {
		viewModel.mu.Unlock()
		return ErrApplyInProgress
	}
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
	viewModel.state.Pending = pending
	err := viewModel.validatePendingLocked()
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
	if viewModel.state.SessionActive || viewModel.state.Phase == PhaseRollbackIncomplete {
		viewModel.mu.Unlock()
		return
	}
	viewModel.state.Pending = nil
	viewModel.state.PendingValid = false
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
	viewModel.state.PendingValid = true
	viewModel.state.Phase = PhaseReviewing
	viewModel.state.LastError = ""
	state, listener := viewModel.stateLocked(), viewModel.listener
	viewModel.mu.Unlock()
	notify(listener, state)
	return append([]ReviewRow(nil), rows...), nil
}

func (viewModel *Tune) CancelReview() {
	viewModel.mu.Lock()
	if viewModel.state.Phase != PhaseReviewing || viewModel.state.SessionActive {
		viewModel.mu.Unlock()
		return
	}
	viewModel.state.Review = nil
	if len(viewModel.state.Pending) > 0 {
		viewModel.state.Phase = PhaseStaged
	} else {
		viewModel.state.Phase = PhaseIdle
	}
	state, listener := viewModel.stateLocked(), viewModel.listener
	viewModel.mu.Unlock()
	notify(listener, state)
}

func (viewModel *Tune) Apply(ctx context.Context) error {
	viewModel.mu.Lock()
	if viewModel.state.Phase == PhaseAuthorizing || viewModel.state.Phase == PhaseApplying || viewModel.state.Phase == PhaseRollingBack {
		viewModel.mu.Unlock()
		return ErrApplyInProgress
	}
	if viewModel.state.SessionActive {
		viewModel.mu.Unlock()
		return ErrSessionActive
	}
	if viewModel.state.Phase != PhaseReviewing || !viewModel.state.PendingValid {
		viewModel.mu.Unlock()
		return ErrReviewRequired
	}
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
		var incomplete *tuning.RollbackError
		if errors.As(err, &incomplete) {
			viewModel.recordIncompleteLocked(incomplete)
		}
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
	if viewModel.state.Phase == PhaseAuthorizing {
		viewModel.state.Phase = PhaseApplying
	}
	state, listener = viewModel.stateLocked(), viewModel.listener
	viewModel.mu.Unlock()
	notify(listener, state)
	if !viewModel.sessionStream {
		go viewModel.consumeEvents(events)
	}
	return nil
}

func (viewModel *Tune) Revert(ctx context.Context) error {
	viewModel.setPhase(PhaseRollingBack, "")
	if err := viewModel.service.Revert(ctx); err != nil {
		viewModel.recordIncomplete(err)
		return err
	}
	viewModel.mu.Lock()
	viewModel.state.Pending = nil
	viewModel.state.PendingValid = false
	viewModel.state.Review = nil
	viewModel.state.Effective = make(map[tuning.ControlID]tuning.Value)
	viewModel.state.SessionActive = false
	viewModel.state.Remaining = nil
	viewModel.state.Unverified = nil
	viewModel.mu.Unlock()
	viewModel.setPhase(PhaseIdle, "")
	return nil
}

func (viewModel *Tune) Close() error {
	viewModel.closeMu.Lock()
	defer viewModel.closeMu.Unlock()
	if viewModel.closed {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := viewModel.service.Close(ctx)
	if err != nil {
		viewModel.recordIncomplete(err)
		return err
	}
	viewModel.Deactivate()
	viewModel.closed = true
	return nil
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
			if len(viewModel.state.Pending) > 0 && viewModel.state.Phase != PhaseRollbackIncomplete {
				if validationErr := viewModel.validatePendingLocked(); validationErr != nil {
					viewModel.state.LastError = validationErr.Error()
				}
			}
			if result.Err != nil && viewModel.state.Phase != PhaseRollbackIncomplete {
				viewModel.state.LastError = result.Err.Error()
			}
			if result.Complete && !viewModel.state.SessionActive && viewModel.state.Phase != PhaseRollbackIncomplete {
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
			viewModel.state.Review = nil
			viewModel.state.Phase = PhaseStaged
			viewModel.state.LastError = ""
			if err := viewModel.validatePendingLocked(); err != nil {
				viewModel.state.LastError = err.Error()
			}
		case "transaction_applied", "applied":
			viewModel.state.Phase = PhaseActive
			viewModel.state.SessionActive = true
		case "rollback_incomplete":
			viewModel.state.Phase = PhaseRollbackIncomplete
			viewModel.state.SessionActive = true
			viewModel.state.LastError = event.Message
			viewModel.state.Remaining = event.Remaining
			viewModel.state.Unverified = append([]tuning.ControlID(nil), event.Unverified...)
		case "session_terminated":
			viewModel.state.Phase = PhaseRollbackIncomplete
			viewModel.state.SessionActive = true
			if viewModel.state.LastError == "" {
				viewModel.state.LastError = event.Message
			}
			if len(viewModel.state.Unverified) == 0 {
				for _, change := range viewModel.state.Pending {
					if _, ok := viewModel.state.Remaining[change.ID]; !ok {
						viewModel.state.Unverified = append(viewModel.state.Unverified, change.ID)
					}
				}
			}
		case "session_closed":
			if !viewModel.state.SessionActive {
				break
			}
			fallthrough
		case "rollback_complete":
			viewModel.state.Phase = PhaseIdle
			viewModel.state.SessionActive = false
			viewModel.state.Pending = nil
			viewModel.state.PendingValid = false
			viewModel.state.Review = nil
			viewModel.state.Remaining = nil
			viewModel.state.Unverified = nil
			viewModel.state.LastError = ""
			viewModel.state.Effective = make(map[tuning.ControlID]tuning.Value)
		case "transaction_failed", "failed":
			viewModel.state.Phase = PhaseFailed
			viewModel.state.SessionActive = false
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
	state.Remaining = make(map[tuning.ControlID]tuning.Value, len(viewModel.state.Remaining))
	for id, value := range viewModel.state.Remaining {
		state.Remaining[id] = cloneValue(value)
	}
	state.Unverified = append([]tuning.ControlID(nil), viewModel.state.Unverified...)
	return state
}

func (viewModel *Tune) recordIncompleteLocked(err *tuning.RollbackError) {
	viewModel.state.Phase = PhaseRollbackIncomplete
	viewModel.state.SessionActive = true
	viewModel.state.LastError = err.Error()
	if err.Remaining != nil || len(err.Unverified) > 0 {
		viewModel.state.Remaining = err.Remaining
		viewModel.state.Unverified = err.Unverified
	}
}
func (viewModel *Tune) recordIncomplete(err error) {
	viewModel.mu.Lock()
	var incomplete *tuning.RollbackError
	if !errors.As(err, &incomplete) {
		incomplete = &tuning.RollbackError{Cause: err}
	}
	viewModel.recordIncompleteLocked(incomplete)
	state, listener := viewModel.stateLocked(), viewModel.listener
	viewModel.mu.Unlock()
	notify(listener, state)
}

func (viewModel *Tune) validatePendingLocked() error {
	if len(viewModel.state.Pending) == 0 {
		viewModel.state.PendingValid = false
		return nil
	}
	set := tuning.ChangeSet{
		Generation: viewModel.state.Capabilities.Generation,
		MachineID:  viewModel.state.Capabilities.MachineID,
		Changes:    append([]tuning.Change(nil), viewModel.state.Pending...),
	}
	_, err := tuning.ValidateChangeSet(viewModel.state.Capabilities, set)
	viewModel.state.PendingValid = err == nil
	return err
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
