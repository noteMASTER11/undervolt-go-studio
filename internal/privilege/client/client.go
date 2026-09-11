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

var ErrHelperIdentity = errors.New("installed helper is incompatible; reinstall the matching Studio helper")

type response struct {
	message protocol.Message
	err     error
}

type Client struct {
	input  io.WriteCloser
	writer *protocol.Writer
	reader *protocol.Reader
	wait   func() error

	writeMu      sync.Mutex
	closeMu      sync.Mutex
	closeResult  error
	mu           sync.Mutex
	pending      map[string]chan response
	closed       bool
	dead         bool
	events       chan tuning.Event
	done         chan struct{}
	lastRollback protocol.Type
	serial       atomic.Uint64

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
	if err == nil {
		err = process.Authorized()
	}
	if err != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = client.Close(closeCtx)
		cancel()
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
		events:  make(chan tuning.Event, 32),
		done:    make(chan struct{}),
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

func (client *Client) Events() <-chan tuning.Event { return client.events }
func (client *Client) Done() <-chan struct{}       { return client.done }

func (client *Client) Handshake(ctx context.Context, build string) (tuning.CapabilitySet, error) {
	hello, err := client.request(ctx, protocol.TypeHello, protocol.HelloPayload{Client: "undervolt-go-studio", Build: build})
	if err != nil {
		return tuning.CapabilitySet{}, errors.Join(ErrHelperIdentity, err)
	}
	var identity protocol.HelperPayload
	if hello.Type != protocol.TypeReady || json.Unmarshal(hello.Payload, &identity) != nil || identity.Build != protocol.HelperBuildIdentity {
		return tuning.CapabilitySet{}, ErrHelperIdentity
	}
	message, err := client.request(ctx, protocol.TypeProbe, protocol.ProbePayload{})
	if err != nil {
		return tuning.CapabilitySet{}, err
	}
	if message.Type != protocol.TypeCapabilities {
		return tuning.CapabilitySet{}, responseError(message)
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
	if client.dead {
		client.mu.Unlock()
		return payload, io.ErrClosedPipe
	}
	client.transactionID = payload.TransactionID
	client.mu.Unlock()
	client.startRenewal()
	return payload, nil
}

func (client *Client) Revert(ctx context.Context) error {
	client.mu.Lock()
	transactionID := client.transactionID
	lastRollback, dead := client.lastRollback, client.dead
	incomplete := lastRollback == protocol.TypeRollbackIncomplete
	client.mu.Unlock()
	if dead && lastRollback != protocol.TypeRollbackComplete {
		return &tuning.RollbackError{Cause: io.ErrClosedPipe}
	}
	if transactionID == "" && !incomplete {
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
	client.closeMu.Lock()
	defer client.closeMu.Unlock()
	client.mu.Lock()
	if client.closed {
		client.mu.Unlock()
		return client.closeResult
	}
	client.closed = true
	transactionID := client.transactionID
	incomplete := client.lastRollback == protocol.TypeRollbackIncomplete
	client.mu.Unlock()
	client.stopRenewal()
	var requestErr error
	if transactionID != "" || incomplete {
		message, err := client.requestAllowClosed(ctx, protocol.TypeClose, protocol.ProbePayload{})
		if err != nil {
			requestErr = err
		} else if message.Type != protocol.TypeRollbackComplete {
			requestErr = responseError(message)
			var incomplete *tuning.RollbackError
			if errors.As(requestErr, &incomplete) {
				client.mu.Lock()
				client.closed = false
				client.mu.Unlock()
				return requestErr
			}
		}
	} else {
		requestErr = client.write(client.message(protocol.TypeClose, protocol.ProbePayload{}))
	}
	closeErr := client.input.Close()
	waitResult := make(chan error, 1)
	go func() { waitResult <- client.wait() }()
	var waitErr error
	select {
	case waitErr = <-waitResult:
	case <-ctx.Done():
		waitErr = ctx.Err()
	}
	client.closeResult = errors.Join(requestErr, closeErr, waitErr)
	return client.closeResult
}

func (client *Client) startRenewal() {
	client.stopRenewal()
	ctx, cancel := context.WithCancel(context.Background())
	client.mu.Lock()
	if client.dead || client.closed {
		client.mu.Unlock()
		cancel()
		return
	}
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
					if err := client.write(client.message(protocol.TypeRenew, protocol.TransactionPayload{TransactionID: transactionID})); err != nil {
						client.failPending(err)
						_ = client.input.Close()
						return
					}
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
	closed := client.closed || client.dead
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
	if client.dead {
		client.mu.Unlock()
		return protocol.Message{}, io.ErrClosedPipe
	}
	client.pending[message.RequestID] = responses
	if messageType == protocol.TypeBegin {
		client.lastRollback = ""
	}
	client.mu.Unlock()
	if err := client.write(message); err != nil {
		client.removePending(message.RequestID)
		client.failPending(err)
		_ = client.input.Close()
		return protocol.Message{}, err
	}
	select {
	case <-ctx.Done():
		client.removePending(message.RequestID)
		_ = client.input.Close()
		client.failPending(ctx.Err())
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
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return client.writer.WriteContext(ctx, message)
}

func (client *Client) readLoop() {
	for {
		message, err := client.reader.Read()
		if err != nil {
			client.failPending(err)
			return
		}
		client.mu.Lock()
		if message.Type == protocol.TypeCapabilities || message.Type == protocol.TypeReviewChanged {
			client.lastRollback = protocol.TypeRollbackComplete
		}
		if message.Type == protocol.TypeRollbackComplete || message.Type == protocol.TypeRollbackIncomplete {
			client.lastRollback = message.Type
		}
		responses := client.pending[message.RequestID]
		delete(client.pending, message.RequestID)
		client.mu.Unlock()
		if message.Type == protocol.TypeRollbackComplete || message.Type == protocol.TypeRollbackIncomplete {
			client.stopRenewal()
		}
		if responses != nil {
			responses <- response{message: message}
		} else {
			event := eventFromMessage(message)
			if message.Type == protocol.TypeRollbackComplete {
				client.clearTransaction()
			} else if message.Type == protocol.TypeRollbackIncomplete {
				client.stopRenewal()
			}
			client.mu.Lock()
			if !client.dead {
				client.enqueueEvent(event)
			}
			client.mu.Unlock()
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
	if client.dead {
		client.mu.Unlock()
		return
	}
	client.dead = true
	client.transactionID = ""
	cancel := client.renewCancel
	client.renewCancel = nil
	pending := client.pending
	client.pending = make(map[string]chan response)
	event := tuning.Event{Kind: "session_terminated", Message: "Helper connection lost; stock restoration could not be verified. Retry recovery or reboot."}
	if client.lastRollback == protocol.TypeRollbackComplete {
		event.Kind = "session_closed"
		event.Message = "Stock settings restored; helper session closed"
	}
	client.enqueueEvent(event)
	close(client.done)
	close(client.events)
	client.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	for _, responses := range pending {
		responses <- response{err: err}
	}
}

func responseError(message protocol.Message) error {
	var payload protocol.FailurePayload
	if message.Type == protocol.TypeReviewChanged {
		var changed protocol.CapabilitiesPayload
		json.Unmarshal(message.Payload, &changed)
		return &ReviewChangedError{Capabilities: changed.Capabilities}
	}
	if message.Type == protocol.TypeRollbackIncomplete {
		json.Unmarshal(message.Payload, &payload)
		return &tuning.RollbackError{Remaining: payload.Remaining, Unverified: payload.Unverified, Cause: errors.New(payload.Message)}
	}
	if err := json.Unmarshal(message.Payload, &payload); err == nil && payload.Message != "" {
		return errors.New(payload.Message)
	}
	return fmt.Errorf("client: unexpected helper response %s", message.Type)
}

type ReviewChangedError struct{ Capabilities tuning.CapabilitySet }

func (err *ReviewChangedError) Error() string {
	return "Capabilities changed; review the current values again"
}

func (client *Client) enqueueEvent(event tuning.Event) {
	select {
	case client.events <- event:
	default:
		select {
		case <-client.events:
		default:
		}
		select {
		case client.events <- event:
		default:
		}
	}
}

func eventFromMessage(message protocol.Message) tuning.Event {
	event := tuning.Event{Kind: string(message.Type)}
	var payload protocol.FailurePayload
	if json.Unmarshal(message.Payload, &payload) == nil {
		event.Message = payload.Message
		event.Effective = payload.Effective
		event.Remaining = payload.Remaining
		event.Unverified = payload.Unverified
	}
	return event
}
