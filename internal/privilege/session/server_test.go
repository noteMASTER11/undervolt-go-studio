package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/noteMASTER11/undervolt-go-studio/internal/privilege/protocol"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
)

func TestServerRollsBackOnEOFAndLeaseExpiry(t *testing.T) {
	for _, cause := range []string{"eof", "lease"} {
		t.Run(cause, func(t *testing.T) {
			backend := &fakeBackend{capabilities: tuning.CapabilitySet{Generation: "g", MachineID: "cpu"}}
			server := NewServer(backend, WithLease(40*time.Millisecond))
			client, serverSide := net.Pipe()
			done := make(chan error, 1)
			go func() {
				done <- server.Run(context.Background(), protocol.NewReaderForDirection(serverSide, protocol.ClientToHelper), protocol.NewWriterForDirection(serverSide, protocol.HelperToClient))
			}()
			writer := protocol.NewWriterForDirection(client, protocol.ClientToHelper)
			reader := protocol.NewReaderForDirection(client, protocol.HelperToClient)
			writeMessage(t, writer, "hello", protocol.TypeHello, protocol.HelloPayload{Client: "test"})
			if _, err := reader.Read(); err != nil {
				t.Fatal(err)
			}
			writeMessage(t, writer, "probe", protocol.TypeProbe, protocol.ProbePayload{})
			if message, err := reader.Read(); err != nil || message.Type != protocol.TypeCapabilities {
				t.Fatalf("probe response=%+v err=%v", message, err)
			}
			writeMessage(t, writer, "begin", protocol.TypeBegin, protocol.BeginPayload{Changes: tuning.ChangeSet{Generation: "g", MachineID: "cpu"}})
			if message, err := reader.Read(); err != nil || message.Type != protocol.TypeApplied {
				t.Fatalf("apply response=%+v err=%v", message, err)
			}
			if cause == "eof" {
				_ = client.Close()
			} else {
				if message, err := reader.Read(); err != nil || message.Type != protocol.TypeRollbackComplete {
					t.Fatalf("lease response=%+v err=%v", message, err)
				}
				_ = client.Close()
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("server did not stop")
			}
			if backend.transaction.rollbackCalls() != 1 {
				t.Fatalf("rollback calls = %d", backend.transaction.rollbackCalls())
			}
		})
	}
}

func TestServerHydratesRecoversAndReprobes(t *testing.T) {
	backend := &fakeBackend{capabilities: tuning.CapabilitySet{Generation: "g", MachineID: "cpu"}}
	server := NewServer(backend)
	client, serverSide := net.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- server.Run(context.Background(), protocol.NewReaderForDirection(serverSide, protocol.ClientToHelper), protocol.NewWriterForDirection(serverSide, protocol.HelperToClient))
	}()
	writer := protocol.NewWriterForDirection(client, protocol.ClientToHelper)
	reader := protocol.NewReaderForDirection(client, protocol.HelperToClient)
	writeMessage(t, writer, "hello", protocol.TypeHello, protocol.HelloPayload{Client: "test"})
	if _, err := reader.Read(); err != nil {
		t.Fatal(err)
	}
	writeMessage(t, writer, "probe", protocol.TypeProbe, protocol.ProbePayload{})
	if _, err := reader.Read(); err != nil {
		t.Fatal(err)
	}
	if got := backend.callLog(); len(got) < 3 || got[0] != "probe" || got[1] != "recover" || got[2] != "probe" {
		t.Fatalf("calls = %v", got)
	}
	writeMessage(t, writer, "close", protocol.TypeClose, protocol.ProbePayload{})
	_ = client.Close()
	<-done
}

func writeMessage(t *testing.T, writer *protocol.Writer, requestID string, messageType protocol.Type, payload any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Write(protocol.Message{Version: protocol.Version, RequestID: requestID, Type: messageType, Payload: raw}); err != nil {
		t.Fatal(err)
	}
}

type fakeBackend struct {
	mu           sync.Mutex
	calls        []string
	capabilities tuning.CapabilitySet
	transaction  *fakeTransaction
	recovery     tuning.RecoveryResult
	drift        bool
}

func (backend *fakeBackend) Recover(context.Context) (tuning.RecoveryResult, error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.calls = append(backend.calls, "recover")
	return backend.recovery, nil
}

func (backend *fakeBackend) Probe(context.Context) (tuning.CapabilitySet, error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.calls = append(backend.calls, "probe")
	if backend.drift && len(backend.calls) >= 4 {
		backend.capabilities.Generation = "changed"
	}
	return backend.capabilities, nil
}

func (backend *fakeBackend) Apply(context.Context, tuning.ChangeSet) (Transaction, tuning.ValidationResult, error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.calls = append(backend.calls, "apply")
	if backend.transaction == nil {
		backend.transaction = &fakeTransaction{id: "tx"}
	}
	return backend.transaction, tuning.ValidationResult{}, nil
}

func (backend *fakeBackend) callLog() []string {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return append([]string(nil), backend.calls...)
}

type fakeTransaction struct {
	mu               sync.Mutex
	id               string
	rollbacks        int
	firstRollbackErr error
}

func (transaction *fakeTransaction) Effective() map[tuning.ControlID]tuning.Value {
	return map[tuning.ControlID]tuning.Value{tuning.ControlPL1: tuning.NumericValue(39.5)}
}

func (transaction *fakeTransaction) ID() string { return transaction.id }
func (transaction *fakeTransaction) Rollback(context.Context) error {
	transaction.mu.Lock()
	defer transaction.mu.Unlock()
	transaction.rollbacks++
	if transaction.rollbacks == 1 {
		return transaction.firstRollbackErr
	}
	return nil
}

func (transaction *fakeTransaction) rollbackCalls() int {
	transaction.mu.Lock()
	defer transaction.mu.Unlock()
	return transaction.rollbacks
}

func TestServerBlocksUnresolvedRecovery(t *testing.T) {
	for _, recovery := range []tuning.RecoveryResult{{StaleBoot: true}, {Discrepancies: []string{"PL1 needs audit"}}} {
		backend := &fakeBackend{capabilities: tuning.CapabilitySet{Generation: "g", MachineID: "cpu"}, recovery: recovery}
		var input, output bytes.Buffer
		writer := protocol.NewWriter(&input)
		writeMessage(t, writer, "hello", protocol.TypeHello, protocol.HelloPayload{Client: "test"})
		writeMessage(t, writer, "probe", protocol.TypeProbe, protocol.ProbePayload{})
		writeMessage(t, writer, "begin", protocol.TypeBegin, protocol.BeginPayload{Changes: tuning.ChangeSet{Generation: "g", MachineID: "cpu"}})
		err := NewServer(backend).Run(context.Background(), protocol.NewReader(&input), protocol.NewWriter(&output))
		if err == nil || backend.transaction != nil {
			t.Fatalf("unresolved recovery allowed apply: %v %+v", err, backend.callLog())
		}
	}
}

func TestServerReprobesBeforeBeginAndReturnsChangedReview(t *testing.T) {
	backend := &fakeBackend{capabilities: tuning.CapabilitySet{Generation: "g", MachineID: "cpu"}, drift: true}
	var input, output bytes.Buffer
	writer := protocol.NewWriter(&input)
	writeMessage(t, writer, "hello", protocol.TypeHello, protocol.HelloPayload{Client: "test"})
	writeMessage(t, writer, "probe", protocol.TypeProbe, protocol.ProbePayload{})
	writeMessage(t, writer, "begin", protocol.TypeBegin, protocol.BeginPayload{Changes: tuning.ChangeSet{Generation: "g", MachineID: "cpu"}})
	_ = NewServer(backend).Run(context.Background(), protocol.NewReader(&input), protocol.NewWriter(&output))
	reader := protocol.NewReader(&output)
	found := false
	for {
		message, err := reader.Read()
		if err != nil {
			break
		}
		if message.Type == protocol.TypeReviewChanged {
			found = true
		}
	}
	if !found || backend.transaction != nil {
		t.Fatalf("drift was not rejected before mutation: %v", backend.callLog())
	}
}

func TestBlockedOutputCannotHoldHardwareAfterApply(t *testing.T) {
	backend := &fakeBackend{capabilities: tuning.CapabilitySet{Generation: "g", MachineID: "cpu"}}
	client, peer := net.Pipe()
	defer client.Close()
	defer peer.Close()
	done := make(chan error, 1)
	go func() {
		done <- NewServer(backend, WithLease(30*time.Millisecond)).Run(context.Background(), protocol.NewReader(peer), protocol.NewWriter(peer))
	}()
	writer, reader := protocol.NewWriter(client), protocol.NewReader(client)
	writeMessage(t, writer, "hello", protocol.TypeHello, protocol.HelloPayload{Client: "test"})
	if _, err := reader.Read(); err != nil {
		t.Fatal(err)
	}
	writeMessage(t, writer, "probe", protocol.TypeProbe, protocol.ProbePayload{})
	if _, err := reader.Read(); err != nil {
		t.Fatal(err)
	}
	writeMessage(t, writer, "begin", protocol.TypeBegin, protocol.BeginPayload{Changes: tuning.ChangeSet{Generation: "g", MachineID: "cpu"}})
	// Client stops draining stdout precisely before the applied response.
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Error("blocked applied response prevented rollback")
		client.Close()
		<-done
	}
	if backend.transaction.rollbackCalls() != 1 {
		t.Fatalf("rollback calls=%d", backend.transaction.rollbackCalls())
	}
}

func TestServerTransmitsEffectiveAndRetryableRollbackOutcome(t *testing.T) {
	backend := &fakeBackend{capabilities: tuning.CapabilitySet{Generation: "g", MachineID: "cpu"}, transaction: &fakeTransaction{id: "tx", firstRollbackErr: &tuning.RollbackError{Cause: errors.New("restore failed"), Remaining: map[tuning.ControlID]tuning.Value{tuning.ControlPL1: tuning.NumericValue(39.5)}}}}
	var input, output bytes.Buffer
	writer := protocol.NewWriter(&input)
	writeMessage(t, writer, "hello", protocol.TypeHello, protocol.HelloPayload{Client: "test"})
	writeMessage(t, writer, "probe", protocol.TypeProbe, protocol.ProbePayload{})
	writeMessage(t, writer, "begin", protocol.TypeBegin, protocol.BeginPayload{Changes: tuning.ChangeSet{Generation: "g", MachineID: "cpu"}})
	writeMessage(t, writer, "revert1", protocol.TypeRevert, protocol.TransactionPayload{TransactionID: "tx"})
	writeMessage(t, writer, "revert2", protocol.TypeRevert, protocol.TransactionPayload{TransactionID: "tx"})
	_ = NewServer(backend).Run(context.Background(), protocol.NewReader(&input), protocol.NewWriter(&output))
	reader := protocol.NewReader(&output)
	applied, incomplete, restored := false, false, false
	for {
		message, err := reader.Read()
		if err != nil {
			break
		}
		switch message.Type {
		case protocol.TypeApplied:
			var p protocol.AppliedPayload
			json.Unmarshal(message.Payload, &p)
			applied = p.Effective[tuning.ControlPL1].Number == 39.5
		case protocol.TypeRollbackIncomplete:
			var p protocol.FailurePayload
			json.Unmarshal(message.Payload, &p)
			incomplete = p.Remaining[tuning.ControlPL1].Number == 39.5 && p.Message != "Stock settings restored"
		case protocol.TypeRollbackComplete:
			restored = true
		}
	}
	if !applied || !incomplete || !restored || backend.transaction.rollbackCalls() != 2 {
		t.Fatalf("applied=%v incomplete=%v retried=%v calls=%d", applied, incomplete, restored, backend.transaction.rollbackCalls())
	}
}

func TestProtocolFailureReportsVerifiedRollback(t *testing.T) {
	backend := &fakeBackend{capabilities: tuning.CapabilitySet{Generation: "g", MachineID: "cpu"}}
	var input, output bytes.Buffer
	writer := protocol.NewWriter(&input)
	writeMessage(t, writer, "hello", protocol.TypeHello, protocol.HelloPayload{Client: "test"})
	writeMessage(t, writer, "probe", protocol.TypeProbe, protocol.ProbePayload{})
	writeMessage(t, writer, "begin", protocol.TypeBegin, protocol.BeginPayload{Changes: tuning.ChangeSet{Generation: "g", MachineID: "cpu"}})
	writeMessage(t, writer, "begin", protocol.TypeRenew, protocol.TransactionPayload{TransactionID: "tx"})
	if err := NewServer(backend).Run(context.Background(), protocol.NewReader(&input), protocol.NewWriter(&output)); err == nil {
		t.Fatal("duplicate ID accepted")
	}
	reader := protocol.NewReader(&output)
	last := protocol.Type("")
	for {
		message, err := reader.Read()
		if err != nil {
			break
		}
		last = message.Type
	}
	if last != protocol.TypeRollbackComplete {
		t.Fatalf("verified restoration not communicated: %s", last)
	}
}
