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
)

func TestClientHandshakeReturnsCapabilities(t *testing.T) {
	clientSide, helperSide := net.Pipe()
	go func() {
		defer helperSide.Close()
		reader := protocol.NewReaderForDirection(helperSide, protocol.ClientToHelper)
		writer := protocol.NewWriterForDirection(helperSide, protocol.HelperToClient)
		_, _ = reader.Read()
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
