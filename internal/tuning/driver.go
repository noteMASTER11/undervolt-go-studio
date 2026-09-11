package tuning

import (
	"context"
	"encoding/json"
)

type Driver interface {
	ID() string
	Probe(context.Context) ([]Capability, error)
	Prepare(context.Context, Change) (PreparedOperation, error)
	Restore(context.Context, ControlID, json.RawMessage) (Value, error)
}

type PreparedOperation interface {
	ControlID() ControlID
	DriverID() string
	Order() int
	Capture(context.Context) (json.RawMessage, error)
	Apply(context.Context) (Value, error)
	Restore(context.Context, json.RawMessage) (Value, error)
}
