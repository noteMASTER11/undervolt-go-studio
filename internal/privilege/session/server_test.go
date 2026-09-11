package session

import (
	"context"
	"encoding/json"
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

func TestServerRecoversBeforeProbe(t *testing.T) {
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
	writeMessage(t, writer, "probe", protocol.TypeProbe, protocol.ProbePayload{})
	if _, err := reader.Read(); err != nil {
		t.Fatal(err)
	}
	if got := backend.callLog(); len(got) < 2 || got[0] != "recover" || got[1] != "probe" {
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
}

func (backend *fakeBackend) Recover(context.Context) (tuning.RecoveryResult, error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.calls = append(backend.calls, "recover")
	return tuning.RecoveryResult{}, nil
}

func (backend *fakeBackend) Probe(context.Context) (tuning.CapabilitySet, error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.calls = append(backend.calls, "probe")
	return backend.capabilities, nil
}

func (backend *fakeBackend) Apply(context.Context, tuning.ChangeSet) (Transaction, tuning.ValidationResult, error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.calls = append(backend.calls, "apply")
	backend.transaction = &fakeTransaction{id: "tx"}
	return backend.transaction, tuning.ValidationResult{}, nil
}

func (backend *fakeBackend) callLog() []string {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return append([]string(nil), backend.calls...)
}

type fakeTransaction struct {
	mu        sync.Mutex
	id        string
	rollbacks int
}

func (transaction *fakeTransaction) ID() string { return transaction.id }
func (transaction *fakeTransaction) Rollback(context.Context) error {
	transaction.mu.Lock()
	defer transaction.mu.Unlock()
	transaction.rollbacks++
	return nil
}

func (transaction *fakeTransaction) rollbackCalls() int {
	transaction.mu.Lock()
	defer transaction.mu.Unlock()
	return transaction.rollbacks
}
