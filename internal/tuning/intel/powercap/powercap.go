package powercap

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/sysfs"
)

const root = "sys/class/powercap"

type constraint struct {
	maximumPath string
	path        string
	current     int64
	maximum     *int64
	scale       float64
	zone        string
}

type Driver struct {
	store sysfs.Store

	mu    sync.RWMutex
	zones map[tuning.ControlID][]constraint
}

func New(store sysfs.Store) *Driver {
	return &Driver{store: store, zones: make(map[tuning.ControlID][]constraint)}
}

func (driver *Driver) ID() string {
	return "intel.powercap"
}

func (driver *Driver) Prepare(ctx context.Context, change tuning.Change) (tuning.PreparedOperation, error) {
	if change.ID == tuning.ControlTau {
		return nil, fmt.Errorf("powercap: representable time windows are unverified")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if change.Requested.Kind != tuning.ValueNumeric {
		return nil, fmt.Errorf("powercap: %s expects a numeric value", change.ID)
	}
	driver.mu.RLock()
	sources := append([]constraint(nil), driver.zones[change.ID]...)
	driver.mu.RUnlock()
	if len(sources) == 0 {
		return nil, fmt.Errorf("powercap: no sources for %s", change.ID)
	}
	return &operation{
		store:     driver.store,
		id:        change.ID,
		sources:   sources,
		requested: change.Requested.Number,
	}, nil
}

func (driver *Driver) Restore(ctx context.Context, id tuning.ControlID, snapshot json.RawMessage) (tuning.Value, error) {
	driver.mu.RLock()
	sources := append([]constraint(nil), driver.zones[id]...)
	driver.mu.RUnlock()
	if len(sources) == 0 {
		return tuning.Value{}, fmt.Errorf("powercap: no sources for %s", id)
	}
	return (&operation{store: driver.store, id: id, sources: sources}).Restore(ctx, snapshot)
}
