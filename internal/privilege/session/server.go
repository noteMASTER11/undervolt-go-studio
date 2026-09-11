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
	Effective() map[tuning.ControlID]tuning.Value
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

type readResult struct {
	message protocol.Message
	err     error
}

func (server *Server) Run(ctx context.Context, reader *protocol.Reader, writer *protocol.Writer) error {
	messages := make(chan readResult, 1)
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		for {
			message, err := reader.Read()
			select {
			case messages <- readResult{message, err}:
			case <-stopped:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	var active Transaction
	var timer *time.Timer
	var lease <-chan time.Time
	greeted, probed := false, false
	seen := make(map[string]struct{})
	stopLease := func() {
		if timer != nil {
			timer.Stop()
		}
		lease = nil
	}
	defer stopLease()
	rollback := func() error {
		stopLease()
		if active == nil {
			return nil
		}
		// Rollback does not inherit cancellation from a dead GUI or signal.
		err := active.Rollback(context.WithoutCancel(ctx))
		if err == nil {
			active = nil
		}
		return err
	}
	rollbackMessage := func(id string, err error) error {
		kind := protocol.TypeRollbackComplete
		payload := protocol.FailurePayload{Message: "Stock settings restored"}
		if err != nil {
			kind = protocol.TypeRollbackIncomplete
			payload = rollbackFailure(err)
		}
		return server.write(writer, id, kind, payload)
	}
	fatal := func(err error, id string) error {
		// Restore before trying to write any error response, even if stdout is full.
		hadActive := active != nil
		restoreErr := rollback()
		if hadActive {
			_ = rollbackMessage(id, restoreErr)
		} else {
			_ = server.write(writer, id, protocol.TypeFailed, protocol.FailurePayload{Message: "Tuning session failed before starting a new transaction"})
		}
		return errors.Join(err, restoreErr)
	}
	for {
		select {
		case <-ctx.Done():
			return errors.Join(ctx.Err(), rollback())
		case <-lease:
			err := rollback()
			writeErr := rollbackMessage("lease", err)
			if writeErr != nil || err == nil {
				return errors.Join(err, writeErr)
			}
			// Keep a failed rollback available for an explicit retry.
		case incoming := <-messages:
			if incoming.err != nil {
				err := rollback()
				if errors.Is(incoming.err, io.EOF) {
					return err
				}
				return errors.Join(incoming.err, err)
			}
			message := incoming.message
			if _, duplicate := seen[message.RequestID]; duplicate {
				return fatal(errors.New("duplicate request ID"), message.RequestID)
			}
			if len(seen) >= 4096 {
				return fatal(errors.New("session request limit reached"), message.RequestID)
			}
			seen[message.RequestID] = struct{}{}
			invalid := func() error {
				return fatal(fmt.Errorf("session: invalid %s transition", message.Type), message.RequestID)
			}
			switch message.Type {
			case protocol.TypeHello:
				if greeted {
					return invalid()
				}
				greeted = true
				if err := server.write(writer, message.RequestID, protocol.TypeReady, protocol.HelperPayload{Build: protocol.HelperBuildIdentity}); err != nil {
					return err
				}
			case protocol.TypeProbe:
				if !greeted || active != nil {
					return invalid()
				}
				// Hydrate source tables, restore matching recovery, then publish stock.
				if _, err := server.backend.Probe(ctx); err != nil {
					_ = server.write(writer, message.RequestID, protocol.TypeRollbackIncomplete, rollbackFailure(err))
					return err
				}
				recovery, err := server.backend.Recover(ctx)
				if recovery.StaleBoot || len(recovery.Discrepancies) > 0 {
					err = errors.Join(err, errors.New("recovery audit is unresolved; new tuning is blocked"))
				}
				if err != nil {
					_ = server.write(writer, message.RequestID, protocol.TypeRollbackIncomplete, rollbackFailure(err))
					return err
				}
				caps, err := server.backend.Probe(ctx)
				if err != nil {
					return fatal(err, message.RequestID)
				}
				if err := server.write(writer, message.RequestID, protocol.TypeCapabilities, protocol.CapabilitiesPayload{Capabilities: caps}); err != nil {
					return err
				}
				probed = true
			case protocol.TypeBegin:
				if !probed || active != nil {
					return invalid()
				}
				var payload protocol.BeginPayload
				json.Unmarshal(message.Payload, &payload)
				caps, err := server.backend.Probe(ctx)
				if err != nil {
					return fatal(err, message.RequestID)
				}
				if caps.Generation != payload.Changes.Generation || caps.MachineID != payload.Changes.MachineID {
					if err := server.write(writer, message.RequestID, protocol.TypeReviewChanged, protocol.CapabilitiesPayload{Capabilities: caps}); err != nil {
						return err
					}
					continue
				}
				transaction, _, err := server.backend.Apply(ctx, payload.Changes)
				active = transaction
				if err != nil {
					if active != nil {
						if writeErr := rollbackMessage(message.RequestID, err); writeErr != nil {
							return errors.Join(err, writeErr)
						}
						continue
					}
					if errors.Is(err, tuning.ErrStaleCapabilities) {
						if err := server.write(writer, message.RequestID, protocol.TypeReviewChanged, protocol.CapabilitiesPayload{Capabilities: caps}); err != nil {
							return err
						}
						continue
					}
					if writeErr := server.write(writer, message.RequestID, protocol.TypeFailed, protocol.FailurePayload{Message: "Apply failed; stock restoration completed"}); writeErr != nil {
						return errors.Join(err, writeErr)
					}
					continue
				}
				if active == nil {
					return fatal(errors.New("backend returned no transaction"), message.RequestID)
				}
				timer = time.NewTimer(server.lease)
				lease = timer.C
				if err := server.write(writer, message.RequestID, protocol.TypeApplied, protocol.AppliedPayload{TransactionID: active.ID(), Effective: active.Effective()}); err != nil {
					return errors.Join(err, rollback())
				}
			case protocol.TypeRenew:
				var payload protocol.TransactionPayload
				json.Unmarshal(message.Payload, &payload)
				if active == nil || lease == nil || payload.TransactionID != active.ID() {
					return invalid()
				}
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(server.lease)
				lease = timer.C
			case protocol.TypeRevert, protocol.TypeClose:
				if !greeted {
					return invalid()
				}
				if message.Type == protocol.TypeRevert && active == nil {
					return invalid()
				}
				err := rollback()
				if writeErr := rollbackMessage(message.RequestID, err); writeErr != nil {
					return errors.Join(err, writeErr)
				}
				if message.Type == protocol.TypeClose && err == nil {
					return nil
				}
			default:
				return invalid()
			}
		}
	}
}

func rollbackFailure(err error) protocol.FailurePayload {
	payload := protocol.FailurePayload{Message: "Stock restoration is incomplete; retry rollback or reboot to reset temporary settings"}
	var outcome *tuning.RollbackError
	if errors.As(err, &outcome) {
		payload.Remaining = outcome.Remaining
		payload.Unverified = outcome.Unverified
	}
	return payload
}

func (server *Server) write(writer *protocol.Writer, id string, kind protocol.Type, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), min(250*time.Millisecond, server.lease))
	defer cancel()
	return writer.WriteContext(ctx, protocol.Message{Version: protocol.Version, RequestID: id, Type: kind, Payload: raw})
}
