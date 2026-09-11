package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/noteMASTER11/undervolt-go-studio/internal/privilege/protocol"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
)

func TestClientHandshakeReturnsCapabilities(t *testing.T) {
	clientSide, helperSide := net.Pipe()
	go func() {
		defer helperSide.Close()
		reader := protocol.NewReaderForDirection(helperSide, protocol.ClientToHelper)
		writer := protocol.NewWriterForDirection(helperSide, protocol.HelperToClient)
		hello, _ := reader.Read()
		identity, _ := json.Marshal(protocol.HelperPayload{Build: protocol.HelperBuildIdentity})
		_ = writer.Write(protocol.Message{Version: protocol.Version, RequestID: hello.RequestID, Type: protocol.TypeReady, Payload: identity})
		probe, _ := reader.Read()
		payload, _ := json.Marshal(protocol.CapabilitiesPayload{})
		_ = writer.Write(protocol.Message{Version: protocol.Version, RequestID: probe.RequestID, Type: protocol.TypeCapabilities, Payload: payload})
		_, _ = reader.Read()
	}()
	client := NewStreamClient(clientSide, clientSide, func() error { return clientSide.Close() }, func() error { return nil })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	capabilities, err := client.Handshake(ctx, "test-build")
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.Generation != "" || capabilities.MachineID != "" {
		t.Fatalf("capabilities = %+v", capabilities)
	}
	if err := client.Close(ctx); err != nil && err != io.EOF {
		t.Fatal(err)
	}
}

func TestHandshakeRejectsIncompatibleHelperIdentityBeforeProbe(t *testing.T) {
	clientSide, helperSide := net.Pipe()
	defer clientSide.Close()
	defer helperSide.Close()
	go func() {
		reader, writer := protocol.NewReader(helperSide), protocol.NewWriter(helperSide)
		hello, _ := reader.Read()
		payload, _ := json.Marshal(protocol.HelperPayload{Build: "stale-unsafe-build"})
		_ = writer.Write(protocol.Message{Version: protocol.Version, RequestID: hello.RequestID, Type: protocol.TypeReady, Payload: payload})
		helperSide.Close()
	}()
	client := NewStreamClient(clientSide, clientSide, nil, func() error { return nil })
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := client.Handshake(ctx, "test"); !errors.Is(err, ErrHelperIdentity) {
		t.Fatalf("stale helper was not classified for installation repair: %v", err)
	}
}

func TestClientDeliversUnsolicitedRollbackAndRejectsDeadSession(t *testing.T) {
	clientSide, helperSide := net.Pipe()
	defer clientSide.Close()
	client := NewStreamClient(clientSide, clientSide, nil, func() error { return nil })
	go func() {
		payload, _ := json.Marshal(protocol.FailurePayload{Message: "Lease expired; stock restored"})
		_ = protocol.NewWriter(helperSide).Write(protocol.Message{Version: protocol.Version, RequestID: "lease", Type: protocol.TypeRollbackComplete, Payload: payload})
		helperSide.Close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	select {
	case event := <-client.Events():
		if event.Kind != "rollback_complete" {
			t.Fatalf("event=%+v", event)
		}
	case <-ctx.Done():
		t.Fatal("lease outcome discarded")
	}
	select {
	case <-client.Done():
	case <-ctx.Done():
		t.Fatal("EOF did not terminate session")
	}
	if _, err := client.Apply(ctx, tuning.ChangeSet{}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("dead client reused: %v", err)
	}
}

func TestRollbackResponseRetainsStructuredFailure(t *testing.T) {
	raw, _ := json.Marshal(protocol.FailurePayload{Message: "Stock restoration incomplete", Remaining: map[tuning.ControlID]tuning.Value{tuning.ControlPL1: tuning.NumericValue(39.5)}})
	err := responseError(protocol.Message{Type: protocol.TypeRollbackIncomplete, Payload: raw})
	var incomplete *tuning.RollbackError
	if !errors.As(err, &incomplete) || incomplete.Remaining[tuning.ControlPL1].Number != 39.5 {
		t.Fatalf("rollback detail lost: %v", err)
	}
}

func TestClientRetriesIncompleteApplyInsteadOfReturningFalseRevertSuccess(t *testing.T) {
	clientSide, helperSide := net.Pipe()
	defer clientSide.Close()
	defer helperSide.Close()
	reverted := make(chan bool, 1)
	go func() {
		reader, writer := protocol.NewReader(helperSide), protocol.NewWriter(helperSide)
		begin, _ := reader.Read()
		raw, _ := json.Marshal(protocol.FailurePayload{Message: "Stock restoration incomplete"})
		writer.Write(protocol.Message{Version: protocol.Version, RequestID: begin.RequestID, Type: protocol.TypeRollbackIncomplete, Payload: raw})
		revert, err := reader.Read()
		reverted <- err == nil && revert.Type == protocol.TypeRevert
		if err == nil {
			writer.Write(protocol.Message{Version: protocol.Version, RequestID: revert.RequestID, Type: protocol.TypeRollbackComplete, Payload: json.RawMessage(`{"message":"Stock settings restored"}`)})
		}
	}()
	client := NewStreamClient(clientSide, clientSide, nil, func() error { return nil })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.Apply(ctx, tuning.ChangeSet{}); err == nil {
		t.Fatal("incomplete apply succeeded")
	}
	if err := client.Revert(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case ok := <-reverted:
		if !ok {
			t.Fatal("no revert sent")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("retry incorrectly claimed success without contacting helper")
	}
}

func TestNewBeginCannotReusePreviousRollbackProofAfterEOF(t *testing.T) {
	clientSide, helperSide := net.Pipe()
	defer clientSide.Close()
	defer helperSide.Close()
	client := NewStreamClient(clientSide, clientSide, nil, func() error { return nil })
	client.mu.Lock()
	client.lastRollback = protocol.TypeRollbackComplete
	client.mu.Unlock()
	go func() { protocol.NewReader(helperSide).Read(); helperSide.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client.Apply(ctx, tuning.ChangeSet{})
	select {
	case event := <-client.Events():
		if event.Kind != "session_terminated" {
			t.Fatalf("old rollback falsely verified new transaction: %+v", event)
		}
	case <-ctx.Done():
		t.Fatal("missing termination")
	}
}

func TestClientCloseBoundsProcessWaitByContext(t *testing.T) {
	clientSide, helperSide := net.Pipe()
	go func() {
		reader := protocol.NewReaderForDirection(helperSide, protocol.ClientToHelper)
		_, _ = reader.Read()
		_ = helperSide.Close()
	}()
	waitForever := make(chan struct{})
	client := NewStreamClient(clientSide, clientSide, func() error { return clientSide.Close() }, func() error {
		<-waitForever
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.Close(ctx) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("close error = %v, want deadline exceeded", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Close remained blocked after its context expired")
	}
}
