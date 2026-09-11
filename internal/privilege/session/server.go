package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/noteMASTER11/undervolt-go-studio/internal/privilege/protocol"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
)

type Transaction interface {
	ID() string
	Rollback(context.Context) error
}

type Backend interface {
	Recover(context.Context) (tuning.RecoveryResult, error)
	Probe(context.Context) (tuning.CapabilitySet, error)
	Apply(context.Context, tuning.ChangeSet) (Transaction, tuning.ValidationResult, error)
}

type Option func(*Server)

func WithLease(duration time.Duration) Option {
	return func(server *Server) { server.lease = duration }
}

type Server struct {
	backend Backend
	lease   time.Duration
}

func NewServer(backend Backend, options ...Option) *Server {
	server := &Server{backend: backend, lease: 10 * time.Second}
	for _, option := range options {
		option(server)
	}
	return server
}

type state uint8

const (
	stateStarting state = iota
	stateGreeted
	stateProbed
	stateActive
)

type readResult struct {
	message protocol.Message
	err     error
}

func (server *Server) Run(ctx context.Context, reader *protocol.Reader, writer *protocol.Writer) error {
	messages := make(chan readResult, 1)
	go func() {
		for {
			message, err := reader.Read()
			messages <- readResult{message: message, err: err}
			if err != nil {
				return
			}
		}
	}()

	currentState := stateStarting
	var active Transaction
	var leaseTimer *time.Timer
	var leaseChannel <-chan time.Time
	stopLease := func() {
		if leaseTimer != nil {
			leaseTimer.Stop()
		}
		leaseChannel = nil
	}
	defer stopLease()
	rollback := func(rollbackContext context.Context) error {
		stopLease()
		if active == nil {
			return nil
		}
		err := active.Rollback(rollbackContext)
		active = nil
		currentState = stateProbed
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return errors.Join(ctx.Err(), rollback(context.WithoutCancel(ctx)))
		case <-leaseChannel:
			err := rollback(context.WithoutCancel(ctx))
			messageType := protocol.TypeRollbackComplete
			if err != nil {
				messageType = protocol.TypeRollbackIncomplete
			}
			writeErr := writePayload(writer, "lease", messageType, protocol.FailurePayload{Message: "Tuning lease expired; stock settings restored"})
			return errors.Join(err, writeErr)
		case incoming := <-messages:
			if incoming.err != nil {
				rollbackErr := rollback(context.WithoutCancel(ctx))
				if errors.Is(incoming.err, io.EOF) {
					return rollbackErr
				}
				return errors.Join(incoming.err, rollbackErr)
			}
			message := incoming.message
			switch message.Type {
			case protocol.TypeHello:
				if currentState != stateStarting {
					return server.invalidTransition(ctx, writer, message, &active)
				}
				currentState = stateGreeted
			case protocol.TypeProbe:
				if currentState != stateGreeted && currentState != stateProbed {
					return server.invalidTransition(ctx, writer, message, &active)
				}
				if _, err := server.backend.Recover(ctx); err != nil {
					_ = writePayload(writer, message.RequestID, protocol.TypeFailed, protocol.FailurePayload{Message: err.Error()})
					return err
				}
				capabilities, err := server.backend.Probe(ctx)
				if err != nil {
					_ = writePayload(writer, message.RequestID, protocol.TypeFailed, protocol.FailurePayload{Message: err.Error()})
					return err
				}
				if err := writePayload(writer, message.RequestID, protocol.TypeCapabilities, protocol.CapabilitiesPayload{Capabilities: capabilities}); err != nil {
					return err
				}
				currentState = stateProbed
			case protocol.TypeBegin:
				if currentState != stateProbed || active != nil {
					return server.invalidTransition(ctx, writer, message, &active)
				}
				var payload protocol.BeginPayload
				if err := json.Unmarshal(message.Payload, &payload); err != nil {
					return err
				}
				transaction, _, err := server.backend.Apply(ctx, payload.Changes)
				if err != nil {
					if errors.Is(err, tuning.ErrStaleCapabilities) {
						capabilities, probeErr := server.backend.Probe(ctx)
						if probeErr != nil {
							return errors.Join(err, probeErr)
						}
						return writePayload(writer, message.RequestID, protocol.TypeReviewChanged, protocol.CapabilitiesPayload{Capabilities: capabilities})
					}
					return writePayload(writer, message.RequestID, protocol.TypeFailed, protocol.FailurePayload{Message: err.Error()})
				}
				active = transaction
				currentState = stateActive
				leaseTimer = time.NewTimer(server.lease)
				leaseChannel = leaseTimer.C
				if err := writePayload(writer, message.RequestID, protocol.TypeApplied, protocol.AppliedPayload{TransactionID: active.ID()}); err != nil {
					return errors.Join(err, rollback(context.WithoutCancel(ctx)))
				}
			case protocol.TypeRenew:
				if currentState != stateActive || active == nil {
					return server.invalidTransition(ctx, writer, message, &active)
				}
				var payload protocol.TransactionPayload
				if err := json.Unmarshal(message.Payload, &payload); err != nil || payload.TransactionID != active.ID() {
					return server.invalidTransition(ctx, writer, message, &active)
				}
				if !leaseTimer.Stop() {
					select {
					case <-leaseTimer.C:
					default:
					}
				}
				leaseTimer.Reset(server.lease)
				leaseChannel = leaseTimer.C
			case protocol.TypeRevert:
				if currentState != stateActive || active == nil {
					return server.invalidTransition(ctx, writer, message, &active)
				}
				err := rollback(context.WithoutCancel(ctx))
				messageType := protocol.TypeRollbackComplete
				if err != nil {
					messageType = protocol.TypeRollbackIncomplete
				}
				if writeErr := writePayload(writer, message.RequestID, messageType, protocol.FailurePayload{Message: "Stock settings restored"}); writeErr != nil {
					return errors.Join(err, writeErr)
				}
				if err != nil {
					return err
				}
			case protocol.TypeClose:
				if active == nil {
					return nil
				}
				err := rollback(context.WithoutCancel(ctx))
				messageType := protocol.TypeRollbackComplete
				if err != nil {
					messageType = protocol.TypeRollbackIncomplete
				}
				writeErr := writePayload(writer, message.RequestID, messageType, protocol.FailurePayload{Message: "Session closed; stock settings restored"})
				return errors.Join(err, writeErr)
			default:
				return server.invalidTransition(ctx, writer, message, &active)
			}
		}
	}
}

func (server *Server) invalidTransition(ctx context.Context, writer *protocol.Writer, message protocol.Message, active *Transaction) error {
	err := fmt.Errorf("session: invalid %s transition", message.Type)
	_ = writePayload(writer, message.RequestID, protocol.TypeFailed, protocol.FailurePayload{Message: err.Error()})
	if *active != nil {
		return errors.Join(err, (*active).Rollback(context.WithoutCancel(ctx)))
	}
	return err
}

func writePayload(writer *protocol.Writer, requestID string, messageType protocol.Type, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return writer.Write(protocol.Message{Version: protocol.Version, RequestID: requestID, Type: messageType, Payload: raw})
}
