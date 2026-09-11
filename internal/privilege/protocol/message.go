package protocol

import (
	"encoding/json"

	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
)

const (
	Version      = 1
	MaxFrameSize = 1 << 20
)

type Type string

const (
	TypeHello              Type = "hello"
	TypeProbe              Type = "probe_privileged"
	TypeBegin              Type = "begin_transaction"
	TypeRenew              Type = "renew_lease"
	TypeRevert             Type = "revert"
	TypeClose              Type = "close_session"
	TypeCapabilities       Type = "capabilities"
	TypeReviewChanged      Type = "review_changed"
	TypeProgress           Type = "transaction_progress"
	TypeApplied            Type = "transaction_applied"
	TypeFailed             Type = "transaction_failed"
	TypeRollbackProgress   Type = "rollback_progress"
	TypeRollbackComplete   Type = "rollback_complete"
	TypeRollbackIncomplete Type = "rollback_incomplete"
)

type Direction uint8

const (
	AnyDirection Direction = iota
	ClientToHelper
	HelperToClient
)

type Message struct {
	Version   int             `json:"version"`
	RequestID string          `json:"request_id"`
	Type      Type            `json:"type"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

type HelloPayload struct {
	Client string `json:"client"`
	Build  string `json:"build,omitempty"`
}

type ProbePayload struct{}

type BeginPayload struct {
	Changes tuning.ChangeSet `json:"changes"`
}

type TransactionPayload struct {
	TransactionID string `json:"transaction_id"`
}

type CapabilitiesPayload struct {
	Capabilities tuning.CapabilitySet `json:"capabilities"`
}

type ProgressPayload struct {
	Event tuning.Event `json:"event"`
}

type AppliedPayload struct {
	TransactionID string                            `json:"transaction_id"`
	Effective     map[tuning.ControlID]tuning.Value `json:"effective,omitempty"`
}

type FailurePayload struct {
	Message   string                            `json:"message"`
	Effective map[tuning.ControlID]tuning.Value `json:"effective,omitempty"`
	Remaining map[tuning.ControlID]tuning.Value `json:"remaining,omitempty"`
}

func knownType(messageType Type) bool {
	switch messageType {
	case TypeHello, TypeProbe, TypeBegin, TypeRenew, TypeRevert, TypeClose,
		TypeCapabilities, TypeReviewChanged, TypeProgress, TypeApplied, TypeFailed,
		TypeRollbackProgress, TypeRollbackComplete, TypeRollbackIncomplete:
		return true
	default:
		return false
	}
}

func validForDirection(messageType Type, direction Direction) bool {
	if direction == AnyDirection {
		return true
	}
	clientMessage := messageType == TypeHello || messageType == TypeProbe || messageType == TypeBegin ||
		messageType == TypeRenew || messageType == TypeRevert || messageType == TypeClose
	if direction == ClientToHelper {
		return clientMessage
	}
	return !clientMessage
}
