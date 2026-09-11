package powercap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"

	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/sysfs"
)

type snapshotRecord struct {
	Path  string `json:"path"`
	Value int64  `json:"value"`
}

type operation struct {
	store     sysfs.Store
	id        tuning.ControlID
	sources   []constraint
	requested float64

	mu       sync.Mutex
	snapshot []snapshotRecord
}

func (operation *operation) ControlID() tuning.ControlID { return operation.id }
func (operation *operation) DriverID() string            { return "intel.powercap" }
func (operation *operation) Order() int                  { return tuning.OrderPower }

func (operation *operation) Remaining(ctx context.Context) (tuning.Value, error) {
	return operation.effectiveValue(ctx)
}

func (operation *operation) Capture(ctx context.Context) (json.RawMessage, error) {
	records := make([]snapshotRecord, 0, len(operation.sources))
	for _, source := range operation.sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		value, err := readInteger(operation.store, source.path)
		if err != nil {
			return nil, fmt.Errorf("powercap: capture %s: %w", source.path, err)
		}
		records = append(records, snapshotRecord{Path: source.path, Value: value})
	}
	operation.mu.Lock()
	operation.snapshot = append([]snapshotRecord(nil), records...)
	operation.mu.Unlock()
	encoded, err := json.Marshal(records)
	return encoded, err
}

func (operation *operation) Apply(ctx context.Context) (tuning.Value, error) {
	operation.mu.Lock()
	snapshot := append([]snapshotRecord(nil), operation.snapshot...)
	operation.mu.Unlock()
	if len(snapshot) != len(operation.sources) {
		return tuning.Value{}, fmt.Errorf("powercap: operation must be captured before apply")
	}

	written := 0
	for index, source := range operation.sources {
		if err := ctx.Err(); err != nil {
			operation.rollbackWritten(snapshot, written)
			return tuning.Value{}, err
		}
		requested := int64(math.Round(operation.requested * source.scale))
		if err := writeAndVerify(operation.store, source.path, requested); err != nil {
			rollbackErr := operation.rollbackWritten(snapshot, written)
			return tuning.Value{}, errors.Join(fmt.Errorf("powercap: apply source %d: %w", index, err), rollbackErr)
		}
		written++
	}
	return operation.effectiveValue(ctx)
}

func (operation *operation) Restore(ctx context.Context, raw json.RawMessage) (tuning.Value, error) {
	var records []snapshotRecord
	if err := json.Unmarshal(raw, &records); err != nil {
		return tuning.Value{}, fmt.Errorf("powercap: decode snapshot: %w", err)
	}
	allowed := make(map[string]struct{}, len(operation.sources))
	for _, source := range operation.sources {
		allowed[source.path] = struct{}{}
	}
	if len(records) != len(allowed) {
		return tuning.Value{}, fmt.Errorf("powercap: snapshot source count changed")
	}
	var restoreErr error
	for index := len(records) - 1; index >= 0; index-- {
		if err := ctx.Err(); err != nil {
			return tuning.Value{}, errors.Join(restoreErr, err)
		}
		record := records[index]
		if _, ok := allowed[record.Path]; !ok {
			return tuning.Value{}, fmt.Errorf("powercap: snapshot contains unexpected path %s", record.Path)
		}
		if err := writeAndVerify(operation.store, record.Path, record.Value); err != nil {
			restoreErr = errors.Join(restoreErr, fmt.Errorf("restore %s: %w", record.Path, err))
		}
	}
	if restoreErr != nil {
		return tuning.Value{}, restoreErr
	}
	return operation.effectiveValue(ctx)
}

func (operation *operation) rollbackWritten(snapshot []snapshotRecord, count int) error {
	var rollbackErr error
	for index := count - 1; index >= 0; index-- {
		if err := writeAndVerify(operation.store, snapshot[index].Path, snapshot[index].Value); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("rollback %s: %w", snapshot[index].Path, err))
		}
	}
	return rollbackErr
}

func (operation *operation) effectiveValue(ctx context.Context) (tuning.Value, error) {
	effective := math.Inf(1)
	for _, source := range operation.sources {
		if err := ctx.Err(); err != nil {
			return tuning.Value{}, err
		}
		value, err := readInteger(operation.store, source.path)
		if err != nil {
			return tuning.Value{}, err
		}
		effective = math.Min(effective, float64(value)/source.scale)
	}
	return tuning.NumericValue(effective), nil
}

func writeAndVerify(store sysfs.Store, path string, value int64) error {
	encoded := []byte(strconv.FormatInt(value, 10))
	if err := store.Write(path, encoded); err != nil {
		return err
	}
	readBack, err := readInteger(store, path)
	if err != nil {
		return err
	}
	if readBack != value {
		return fmt.Errorf("read-back mismatch: wrote %d, read %d", value, readBack)
	}
	return nil
}

func readInteger(store sysfs.Store, path string) (int64, error) {
	raw, err := store.Read(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
}
