package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/noteMASTER11/undervolt-go-studio/internal/events"
	privilegeclient "github.com/noteMASTER11/undervolt-go-studio/internal/privilege/client"
	"github.com/noteMASTER11/undervolt-go-studio/internal/privilege/protocol"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/intel"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/intel/policy"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/intel/powercap"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/intel/thermal"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/sysfs"
)

var (
	ErrTuningApplyInProgress = errors.New("a privileged tuning apply is already in progress")
	ErrTuningClosed          = errors.New("the tuning service is closed")
)

type tuneSession interface {
	Apply(context.Context, tuning.ChangeSet) (protocol.AppliedPayload, error)
	Revert(context.Context) error
	Close(context.Context) error
}

type tuneDial func(context.Context, string) (tuneSession, tuning.CapabilitySet, error)

type desktopTuneService struct {
	discoverer *tuning.Discoverer
	build      string
	dial       tuneDial
	eventStore *events.Store

	applyMu     sync.Mutex
	mu          sync.Mutex
	client      tuneSession
	privileged  tuning.CapabilitySet
	transaction string
	applyCancel context.CancelFunc
	applyDone   chan struct{}
	closed      bool
}

func newDesktopTuneService(build string, eventStore *events.Store) *desktopTuneService {
	identity, _ := intel.DetectIdentity()
	machineID := fmt.Sprintf("%s-%d-%x-%d", identity.Vendor, identity.Family, identity.Model, identity.Stepping)
	store := sysfs.RootStore{Root: "/"}
	return &desktopTuneService{
		build: build, eventStore: eventStore,
		dial: func(ctx context.Context, build string) (tuneSession, tuning.CapabilitySet, error) {
			return privilegeclient.Dial(ctx, privilegeclient.PKExecLauncher{}, build)
		},
		discoverer: tuning.NewDiscoverer(machineID,
			powercap.New(store),
			policy.New(store),
			thermal.New(store),
			deferredIntelControls{},
		),
	}
}

type deferredIntelControls struct{}

func (deferredIntelControls) ID() string { return "intel.msr" }

func (deferredIntelControls) Probe(ctx context.Context) ([]tuning.Capability, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	const reason = "Authentication is required to inspect this processor register safely"
	return []tuning.Capability{
		{ID: tuning.ControlRatioPCore, Label: "P-core turbo ratios", Scope: "Per active P-core count", Unit: tuning.UnitRatio, State: tuning.StateRequiresProbe, Reason: reason, RequiresPrivilege: true},
		{ID: tuning.ControlRatioECore, Label: "E-core turbo ratios", Scope: "Efficiency cores", Unit: tuning.UnitRatio, State: tuning.StateRequiresProbe, Reason: reason, RequiresPrivilege: true},
		{ID: tuning.ControlVoltageCore, Label: "Core voltage offset", Scope: "CPU core plane", Unit: tuning.UnitMilliVolt, State: tuning.StateRequiresProbe, Reason: reason, RequiresPrivilege: true},
		{ID: tuning.ControlVoltageCache, Label: "Cache voltage offset", Scope: "CPU cache plane", Unit: tuning.UnitMilliVolt, State: tuning.StateRequiresProbe, Reason: reason, RequiresPrivilege: true},
	}, nil
}

func (deferredIntelControls) Prepare(context.Context, tuning.Change) (tuning.PreparedOperation, error) {
	return nil, errors.New("privileged processor probe is required")
}

func (deferredIntelControls) Restore(context.Context, tuning.ControlID, json.RawMessage) (tuning.Value, error) {
	return tuning.Value{}, errors.New("privileged processor probe is required")
}

func (service *desktopTuneService) Discover(ctx context.Context) <-chan tuning.DiscoveryResult {
	service.mu.Lock()
	privileged := service.privileged
	service.mu.Unlock()
	if privileged.Generation == "" {
		return service.discoverer.Discover(ctx)
	}
	results := make(chan tuning.DiscoveryResult, 1)
	results <- tuning.DiscoveryResult{Set: privileged, Complete: true}
	close(results)
	return results
}

func (service *desktopTuneService) Apply(ctx context.Context, changes tuning.ChangeSet) (<-chan tuning.Event, error) {
	if !service.applyMu.TryLock() {
		return nil, ErrTuningApplyInProgress
	}
	defer service.applyMu.Unlock()

	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return nil, ErrTuningClosed
	}
	operationContext, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	service.applyCancel = cancel
	service.applyDone = done
	client := service.client
	service.mu.Unlock()
	defer func() {
		cancel()
		service.mu.Lock()
		if service.applyDone == done {
			service.applyCancel = nil
			service.applyDone = nil
		}
		close(done)
		service.mu.Unlock()
	}()

	if client == nil {
		dial := service.dial
		if dial == nil {
			dial = func(ctx context.Context, build string) (tuneSession, tuning.CapabilitySet, error) {
				return privilegeclient.Dial(ctx, privilegeclient.PKExecLauncher{}, build)
			}
		}
		connected, capabilities, err := dial(operationContext, service.build)
		if err != nil {
			return nil, err
		}
		service.mu.Lock()
		if service.closed {
			service.mu.Unlock()
			closeTuneSession(connected)
			return nil, ErrTuningClosed
		}
		service.client = connected
		service.privileged = capabilities
		client = connected
		service.mu.Unlock()
		if capabilities.Generation != changes.Generation {
			return service.publish(tuning.Event{Kind: "review_changed", Message: "Privileged capability check changed the review", Capabilities: &capabilities}), nil
		}
	}
	applied, err := client.Apply(operationContext, changes)
	if err != nil {
		return nil, err
	}
	service.mu.Lock()
	if service.closed {
		if service.client == client {
			service.client = nil
		}
		service.mu.Unlock()
		closeTuneSession(client)
		return nil, ErrTuningClosed
	}
	service.transaction = applied.TransactionID
	service.mu.Unlock()
	return service.publish(tuning.Event{Kind: "transaction_applied", Message: "Temporary tuning session is active", Effective: applied.Effective}), nil
}

func (service *desktopTuneService) Revert(ctx context.Context) error {
	if !service.applyMu.TryLock() {
		return ErrTuningApplyInProgress
	}
	defer service.applyMu.Unlock()

	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return ErrTuningClosed
	}
	operationContext, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	service.applyCancel = cancel
	service.applyDone = done
	client := service.client
	service.mu.Unlock()
	defer func() {
		cancel()
		service.mu.Lock()
		if service.applyDone == done {
			service.applyCancel = nil
			service.applyDone = nil
		}
		close(done)
		service.mu.Unlock()
	}()
	if client == nil {
		return nil
	}
	err := client.Revert(operationContext)
	if err == nil {
		service.mu.Lock()
		service.transaction = ""
		service.mu.Unlock()
	}
	return err
}

func (service *desktopTuneService) Close(ctx context.Context) error {
	service.mu.Lock()
	service.closed = true
	cancel := service.applyCancel
	done := service.applyDone
	service.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	service.mu.Lock()
	client := service.client
	service.client = nil
	service.transaction = ""
	service.mu.Unlock()
	if client == nil {
		return nil
	}
	return client.Close(ctx)
}

func closeTuneSession(client tuneSession) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = client.Close(ctx)
}

func (service *desktopTuneService) publish(event tuning.Event) <-chan tuning.Event {
	if service.eventStore != nil {
		service.eventStore.Append(event)
	}
	stream := make(chan tuning.Event, 1)
	stream <- event
	close(stream)
	return stream
}
