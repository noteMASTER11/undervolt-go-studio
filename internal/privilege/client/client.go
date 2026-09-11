package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/noteMASTER11/undervolt-go-studio/internal/privilege/protocol"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
)

type Launcher interface {
	Start(context.Context) (*Process, error)
}

type response struct {
	message protocol.Message
	err     error
}

type Client struct {
	input  io.WriteCloser
	writer *protocol.Writer
	reader *protocol.Reader
	wait   func() error

	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[string]chan response
	closed  bool
	serial  atomic.Uint64

	transactionID string
	renewCancel   context.CancelFunc
}

func Dial(ctx context.Context, launcher Launcher, build string) (*Client, tuning.CapabilitySet, error) {
	process, err := launcher.Start(ctx)
	if err != nil {
		return nil, tuning.CapabilitySet{}, err
	}
	go func() { _, _ = io.Copy(io.Discard, process.Stderr) }()
	client := NewStreamClient(process.Input, process.Output, process.CloseInput, process.Wait)
	capabilities, err := client.Handshake(ctx, build)
	if err != nil {
		_ = client.Close(context.WithoutCancel(ctx))
		return nil, tuning.CapabilitySet{}, err
	}
	return client, capabilities, nil
}

func NewStreamClient(input io.WriteCloser, output io.Reader, closeInput func() error, wait func() error) *Client {
	client := &Client{
		input:   input,
		writer:  protocol.NewWriterForDirection(input, protocol.ClientToHelper),
		reader:  protocol.NewReaderForDirection(output, protocol.HelperToClient),
		wait:    wait,
		pending: make(map[string]chan response),
	}
	if closeInput != nil {
		client.input = closeAdapter{WriteCloser: input, close: closeInput}
		client.writer = protocol.NewWriterForDirection(client.input, protocol.ClientToHelper)
	}
	go client.readLoop()
	return client
}

type closeAdapter struct {
	io.WriteCloser
	close func() error
}

func (adapter closeAdapter) Close() error { return adapter.close() }

func (client *Client) Handshake(ctx context.Context, build string) (tuning.CapabilitySet, error) {
	if err := client.write(client.message(protocol.TypeHello, protocol.HelloPayload{Client: "undervolt-go-studio", Build: build})); err != nil {
		return tuning.CapabilitySet{}, err
	}
	message, err := client.request(ctx, protocol.TypeProbe, protocol.ProbePayload{})
	if err != nil {
		return tuning.CapabilitySet{}, err
	}
	if message.Type != protocol.TypeCapabilities {
		return tuning.CapabilitySet{}, fmt.Errorf("client: expected capabilities, got %s", message.Type)
	}
	var payload protocol.CapabilitiesPayload
	if err := json.Unmarshal(message.Payload, &payload); err != nil {
		return tuning.CapabilitySet{}, err
	}
	return payload.Capabilities, nil
}

func (client *Client) Apply(ctx context.Context, changes tuning.ChangeSet) (protocol.AppliedPayload, error) {
	message, err := client.request(ctx, protocol.TypeBegin, protocol.BeginPayload{Changes: changes})
	if err != nil {
		return protocol.AppliedPayload{}, err
	}
	if message.Type != protocol.TypeApplied {
		return protocol.AppliedPayload{}, responseError(message)
	}
	var payload protocol.AppliedPayload
	if err := json.Unmarshal(message.Payload, &payload); err != nil {
		return payload, err
	}
	client.mu.Lock()
	client.transactionID = payload.TransactionID
	client.mu.Unlock()
	client.startRenewal()
	return payload, nil
}

func (client *Client) Revert(ctx context.Context) error {
	client.mu.Lock()
	transactionID := client.transactionID
	client.mu.Unlock()
	if transactionID == "" {
		return nil
	}
	message, err := client.request(ctx, protocol.TypeRevert, protocol.TransactionPayload{TransactionID: transactionID})
	if err != nil {
		return err
	}
	if message.Type != protocol.TypeRollbackComplete {
		return responseError(message)
	}
	client.clearTransaction()
	return nil
}

func (client *Client) Close(ctx context.Context) error {
	client.mu.Lock()
	if client.closed {
		client.mu.Unlock()
		return nil
	}
	client.closed = true
	transactionID := client.transactionID
	client.mu.Unlock()
	client.stopRenewal()
	var requestErr error
	if transactionID != "" {
		message, err := client.requestAllowClosed(ctx, protocol.TypeClose, protocol.ProbePayload{})
		if err != nil {
			requestErr = err
		} else if message.Type != protocol.TypeRollbackComplete {
			requestErr = responseError(message)
		}
	} else {
		requestErr = client.write(client.message(protocol.TypeClose, protocol.ProbePayload{}))
	}
	closeErr := client.input.Close()
	waitErr := client.wait()
	return errors.Join(requestErr, closeErr, waitErr)
}

func (client *Client) startRenewal() {
	client.stopRenewal()
	ctx, cancel := context.WithCancel(context.Background())
	client.mu.Lock()
	client.renewCancel = cancel
	client.mu.Unlock()
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				client.mu.Lock()
				transactionID := client.transactionID
				client.mu.Unlock()
				if transactionID != "" {
					_ = client.write(client.message(protocol.TypeRenew, protocol.TransactionPayload{TransactionID: transactionID}))
				}
			}
		}
	}()
}

func (client *Client) stopRenewal() {
	client.mu.Lock()
	cancel := client.renewCancel
	client.renewCancel = nil
	client.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (client *Client) clearTransaction() {
	client.stopRenewal()
	client.mu.Lock()
	client.transactionID = ""
	client.mu.Unlock()
}

func (client *Client) request(ctx context.Context, messageType protocol.Type, payload any) (protocol.Message, error) {
	client.mu.Lock()
	closed := client.closed
	client.mu.Unlock()
	if closed {
		return protocol.Message{}, io.ErrClosedPipe
	}
	return client.requestAllowClosed(ctx, messageType, payload)
}

func (client *Client) requestAllowClosed(ctx context.Context, messageType protocol.Type, payload any) (protocol.Message, error) {
	message := client.message(messageType, payload)
	responses := make(chan response, 1)
	client.mu.Lock()
	client.pending[message.RequestID] = responses
	client.mu.Unlock()
	if err := client.write(message); err != nil {
		client.removePending(message.RequestID)
		return protocol.Message{}, err
	}
	select {
	case <-ctx.Done():
		client.removePending(message.RequestID)
		return protocol.Message{}, ctx.Err()
	case result := <-responses:
		return result.message, result.err
	}
}

func (client *Client) message(messageType protocol.Type, payload any) protocol.Message {
	raw, _ := json.Marshal(payload)
	return protocol.Message{Version: protocol.Version, RequestID: fmt.Sprintf("r-%d", client.serial.Add(1)), Type: messageType, Payload: raw}
}

func (client *Client) write(message protocol.Message) error {
	client.writeMu.Lock()
	defer client.writeMu.Unlock()
	return client.writer.Write(message)
}

func (client *Client) readLoop() {
	for {
		message, err := client.reader.Read()
		if err != nil {
			client.failPending(err)
			return
		}
		client.mu.Lock()
		responses := client.pending[message.RequestID]
		delete(client.pending, message.RequestID)
		client.mu.Unlock()
		if responses != nil {
			responses <- response{message: message}
		}
	}
}

func (client *Client) removePending(requestID string) {
	client.mu.Lock()
	delete(client.pending, requestID)
	client.mu.Unlock()
}

func (client *Client) failPending(err error) {
	client.mu.Lock()
	pending := client.pending
	client.pending = make(map[string]chan response)
	client.mu.Unlock()
	for _, responses := range pending {
		responses <- response{err: err}
	}
}

func responseError(message protocol.Message) error {
	var payload protocol.FailurePayload
	if err := json.Unmarshal(message.Payload, &payload); err == nil && payload.Message != "" {
		return errors.New(payload.Message)
	}
	return fmt.Errorf("client: unexpected helper response %s", message.Type)
}
