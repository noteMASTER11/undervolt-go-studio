package ui

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/noteMASTER11/undervolt-go-studio/internal/events"
	"github.com/noteMASTER11/undervolt-go-studio/internal/privilege/protocol"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
)

func TestDesktopTuneServiceRejectsConcurrentApply(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	service := &desktopTuneService{dial: func(ctx context.Context, _ string) (tuneSession, tuning.CapabilitySet, error) {
		close(started)
		select {
		case <-ctx.Done():
			return nil, tuning.CapabilitySet{}, ctx.Err()
		case <-release:
			return &fakeTuneSession{}, tuning.CapabilitySet{Generation: "g"}, nil
		}
	}}
	first := make(chan error, 1)
	go func() {
		_, err := service.Apply(context.Background(), tuning.ChangeSet{Generation: "g"})
		first <- err
	}()
	<-started
	if _, err := service.Apply(context.Background(), tuning.ChangeSet{Generation: "g"}); !errors.Is(err, ErrTuningApplyInProgress) {
		t.Fatalf("second apply error = %v", err)
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
}

func TestDesktopTuneServiceClosePreventsLateClientInstallation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	client := &fakeTuneSession{}
	service := &desktopTuneService{dial: func(context.Context, string) (tuneSession, tuning.CapabilitySet, error) {
		close(started)
		<-release
		return client, tuning.CapabilitySet{Generation: "g"}, nil
	}}
	applyDone := make(chan error, 1)
	go func() {
		_, err := service.Apply(context.Background(), tuning.ChangeSet{Generation: "g"})
		applyDone <- err
	}()
	<-started
	closeContext, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := service.Close(closeContext); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close error = %v, want deadline exceeded", err)
	}
	close(release)
	if err := <-applyDone; !errors.Is(err, ErrTuningClosed) {
		t.Fatalf("late apply error = %v", err)
	}
	service.mu.Lock()
	installed := service.client
	service.mu.Unlock()
	if installed != nil {
		t.Fatal("client was installed after close")
	}
	if client.closeCount() != 1 {
		t.Fatalf("late client closes = %d, want 1", client.closeCount())
	}
}

func TestDeferredIntelControlsExposePrivilegedProbeStates(t *testing.T) {
	capabilities, err := (deferredIntelControls{}).Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[tuning.ControlID]tuning.Unit{
		tuning.ControlRatioPCore:   tuning.UnitRatio,
		tuning.ControlRatioECore:   tuning.UnitRatio,
		tuning.ControlVoltageCore:  tuning.UnitMilliVolt,
		tuning.ControlVoltageCache: tuning.UnitMilliVolt,
	}
	if len(capabilities) != len(want) {
		t.Fatalf("deferred capabilities = %d, want %d", len(capabilities), len(want))
	}
	for _, capability := range capabilities {
		if capability.State != tuning.StateRequiresProbe || !capability.RequiresPrivilege || want[capability.ID] != capability.Unit {
			t.Fatalf("deferred capability = %+v", capability)
		}
		delete(want, capability.ID)
	}
	if len(want) != 0 {
		t.Fatalf("missing deferred controls = %v", want)
	}
}

func TestDesktopTuneServicePublishesSemanticApplyEvent(t *testing.T) {
	store := events.NewStore(10)
	service := &desktopTuneService{client: &fakeTuneSession{}, eventStore: store}
	stream, err := service.Apply(context.Background(), tuning.ChangeSet{Generation: "g"})
	if err != nil {
		t.Fatal(err)
	}
	if event := <-stream; event.Kind != "transaction_applied" {
		t.Fatalf("stream event = %+v", event)
	}
	logged := store.Snapshot()
	if len(logged) != 1 || logged[0].Kind != "transaction_applied" {
		t.Fatalf("logged events = %+v", logged)
	}
}

func TestDesktopTuneServiceCloseCancelsAndJoinsRevert(t *testing.T) {
	client := &fakeTuneSession{revertStarted: make(chan struct{}), revertRelease: make(chan struct{})}
	service := &desktopTuneService{client: client}
	revertDone := make(chan error, 1)
	go func() { revertDone <- service.Revert(context.Background()) }()
	select {
	case <-client.revertStarted:
	case <-time.After(time.Second):
		t.Fatal("revert did not start")
	}
	closeContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.Close(closeContext); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-revertDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("revert error = %v, want cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not join revert")
	}
	if client.closeCount() != 1 {
		t.Fatalf("client closes = %d, want 1", client.closeCount())
	}
	if err := service.Revert(context.Background()); !errors.Is(err, ErrTuningClosed) {
		t.Fatalf("revert after close error = %v", err)
	}
}

type fakeTuneSession struct {
	mu            sync.Mutex
	closes        int
	revertStarted chan struct{}
	revertRelease chan struct{}
	closeErr      error
}

func (*fakeTuneSession) Apply(context.Context, tuning.ChangeSet) (protocol.AppliedPayload, error) {
	return protocol.AppliedPayload{TransactionID: "tx"}, nil
}

func (session *fakeTuneSession) Revert(ctx context.Context) error {
	if session.revertStarted != nil {
		close(session.revertStarted)
	}
	if session.revertRelease != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-session.revertRelease:
		}
	}
	return nil
}

func (session *fakeTuneSession) Close(context.Context) error {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.closes++
	return session.closeErr
}

type eventTuneSession struct {
	fakeTuneSession
	stream chan tuning.Event
}

func (session *eventTuneSession) Events() <-chan tuning.Event { return session.stream }

func TestDesktopTuneServiceForwardsTerminationAndDropsDeadClient(t *testing.T) {
	client := &eventTuneSession{stream: make(chan tuning.Event, 1)}
	service := &desktopTuneService{dial: func(context.Context, string) (tuneSession, tuning.CapabilitySet, error) {
		return client, tuning.CapabilitySet{Generation: "g"}, nil
	}}
	stream := service.Events()
	if _, err := service.Apply(context.Background(), tuning.ChangeSet{Generation: "g"}); err != nil {
		t.Fatal(err)
	}
	if event := <-stream; event.Kind != "transaction_applied" {
		t.Fatalf("initial event=%+v", event)
	}
	client.stream <- tuning.Event{Kind: "session_terminated", Message: "Lost helper"}
	close(client.stream)
	select {
	case event := <-stream:
		if event.Kind != "session_terminated" {
			t.Fatalf("event=%+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("termination discarded")
	}
	service.mu.Lock()
	cached := service.client
	service.mu.Unlock()
	if cached != nil {
		t.Fatal("dead client retained for next apply")
	}
}

func TestDesktopTuneServiceRetainsClientOnIncompleteClose(t *testing.T) {
	client := &fakeTuneSession{closeErr: &tuning.RollbackError{Cause: errors.New("restore failed")}}
	service := &desktopTuneService{client: client}
	if err := service.Close(context.Background()); err == nil {
		t.Fatal("close failure discarded")
	}
	service.mu.Lock()
	cached, closed := service.client, service.closed
	service.mu.Unlock()
	if cached != client || closed {
		t.Fatal("rollback retry lost after close failure")
	}
}

func TestDesktopCloseDoesNotDiscardRecoveryAfterHelperLoss(t *testing.T) {
	service := &desktopTuneService{recoveryNeeded: true}
	if err := service.Close(context.Background()); err == nil {
		t.Fatal("window could close with unverified recovery")
	}
}

func TestDesktopTuneServiceRetriesRecoveryAfterFailedStartupHandshake(t *testing.T) {
	recovered := &fakeTuneSession{}
	dialCount := 0
	service := &desktopTuneService{dial: func(context.Context, string) (tuneSession, tuning.CapabilitySet, error) {
		dialCount++
		if dialCount == 1 {
			return nil, tuning.CapabilitySet{}, &tuning.RollbackError{Cause: errors.New("startup recovery failed")}
		}
		return recovered, tuning.CapabilitySet{Generation: "g"}, nil
	}}

	if _, err := service.Apply(context.Background(), tuning.ChangeSet{Generation: "g"}); err == nil {
		t.Fatal("startup recovery failure was discarded")
	}
	if err := service.Close(context.Background()); err == nil {
		t.Fatal("window could close after unverified startup recovery")
	}
	if dialCount != 1 {
		t.Fatalf("close retried authorization unexpectedly: dials = %d", dialCount)
	}
	if err := service.Revert(context.Background()); err != nil {
		t.Fatalf("retry recovery: %v", err)
	}
	if dialCount != 2 {
		t.Fatalf("retry dials = %d, want 2", dialCount)
	}
	if recovered.closeCount() != 1 {
		t.Fatalf("recovered session closes = %d, want 1", recovered.closeCount())
	}
	service.mu.Lock()
	needsRecovery := service.recoveryNeeded
	service.mu.Unlock()
	if needsRecovery {
		t.Fatal("verified recovery remained armed")
	}
}

func (session *fakeTuneSession) closeCount() int {
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.closes
}
