package thermal

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/sysfs"
)

const (
	coolingRoot = "sys/class/thermal"
	hwmonRoot   = "sys/class/hwmon"
	pciRoot     = "sys/bus/pci/devices"
)

var pciTCCPath = pciRoot + "/0000:00:04.0/tcc_offset_degree_celsius"

type Driver struct {
	store sysfs.Store

	mu           sync.RWMutex
	coolingState string
	pciAttribute string
	tjMax        int64
	maxOffset    int64
}

func New(store sysfs.Store) *Driver { return &Driver{store: store} }
func (driver *Driver) ID() string   { return "intel.tcc" }

func (driver *Driver) Probe(ctx context.Context) ([]tuning.Capability, error) {
	coolingState, maxOffset, coolingOffset, err := driver.findCoolingDevice(ctx)
	if err != nil {
		return nil, err
	}
	pciAttribute, pciOffset, err := driver.findPCIAttribute(ctx)
	if err != nil {
		return nil, err
	}
	tjMax, reference, err := driver.findTjMax(ctx)
	if err != nil {
		return nil, err
	}
	driver.mu.Lock()
	driver.coolingState = coolingState
	driver.pciAttribute = pciAttribute
	driver.tjMax = tjMax
	driver.maxOffset = maxOffset
	driver.mu.Unlock()

	state := tuning.StateSupported
	reasonCode, reason := "", ""
	if coolingOffset != pciOffset {
		state = tuning.StateReadOnly
		reasonCode = "kernel_interface_mismatch"
		reason = fmt.Sprintf("TCC cooling state reports %d °C offset while PCI reports %d °C", coolingOffset, pciOffset)
	}
	minimum := math.Max(0, float64(tjMax-maxOffset))
	return []tuning.Capability{{
		SourceRevision:    sysfs.Revision(driver.store, coolingState, pciAttribute, reference),
		ID:                tuning.ControlThermalLimit,
		Scope:             "CPU package",
		Label:             "Thermal limit",
		Unit:              tuning.UnitCelsius,
		State:             state,
		Current:           tuning.NumericValue(float64(tjMax - pciOffset)),
		Range:             &tuning.NumericRange{Minimum: minimum, Maximum: float64(tjMax), Step: 1},
		DriverID:          driver.ID(),
		ReasonCode:        reasonCode,
		Reason:            reason,
		RequiresPrivilege: true,
		ObservedAt:        time.Now(),
	}}, nil
}

func (driver *Driver) Prepare(ctx context.Context, change tuning.Change) (tuning.PreparedOperation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if change.ID != tuning.ControlThermalLimit || change.Requested.Kind != tuning.ValueNumeric {
		return nil, fmt.Errorf("tcc: invalid change for %s", change.ID)
	}
	driver.mu.RLock()
	operation := &tccOperation{
		store:        driver.store,
		coolingPath:  driver.coolingState,
		pciPath:      driver.pciAttribute,
		tjMax:        driver.tjMax,
		maxOffset:    driver.maxOffset,
		requestedTCC: change.Requested.Number,
	}
	driver.mu.RUnlock()
	if operation.coolingPath == "" || operation.pciPath == "" {
		return nil, fmt.Errorf("tcc: capability has not been discovered")
	}
	return operation, nil
}

func (driver *Driver) Restore(ctx context.Context, id tuning.ControlID, raw json.RawMessage) (tuning.Value, error) {
	if id != tuning.ControlThermalLimit {
		return tuning.Value{}, fmt.Errorf("tcc: unsupported control %s", id)
	}
	driver.mu.RLock()
	operation := &tccOperation{store: driver.store, coolingPath: driver.coolingState, pciPath: driver.pciAttribute, tjMax: driver.tjMax, maxOffset: driver.maxOffset}
	driver.mu.RUnlock()
	return operation.Restore(ctx, raw)
}

func (driver *Driver) findCoolingDevice(ctx context.Context) (string, int64, int64, error) {
	entries, err := driver.store.List(coolingRoot)
	if err != nil {
		return "", 0, 0, fmt.Errorf("tcc: enumerate cooling devices: %w", err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return "", 0, 0, err
		}
		if !strings.HasPrefix(entry, "cooling_device") {
			continue
		}
		base := coolingRoot + "/" + entry
		deviceType, readErr := readTrimmed(driver.store, base+"/type")
		if readErr != nil || deviceType != "TCC Offset" {
			continue
		}
		maxOffset, maxErr := readInteger(driver.store, base+"/max_state")
		currentOffset, currentErr := readInteger(driver.store, base+"/cur_state")
		if maxErr != nil || currentErr != nil {
			return "", 0, 0, fmt.Errorf("tcc: invalid cooling device state")
		}
		return base + "/cur_state", maxOffset, currentOffset, nil
	}
	return "", 0, 0, fmt.Errorf("tcc: TCC Offset cooling device not found")
}

func (driver *Driver) findPCIAttribute(ctx context.Context) (string, int64, error) {
	entries, err := driver.store.List(pciRoot)
	if err != nil {
		return "", 0, fmt.Errorf("tcc: enumerate PCI devices: %w", err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return "", 0, err
		}
		path := pciRoot + "/" + entry + "/tcc_offset_degree_celsius"
		value, readErr := readInteger(driver.store, path)
		if readErr == nil {
			return path, value, nil
		}
	}
	return "", 0, fmt.Errorf("tcc: PCI TCC offset attribute not found")
}

func (driver *Driver) findTjMax(ctx context.Context) (int64, string, error) {
	entries, err := driver.store.List(hwmonRoot)
	if err != nil {
		return 0, "", fmt.Errorf("tcc: enumerate hwmon: %w", err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return 0, "", err
		}
		base := hwmonRoot + "/" + entry
		name, readErr := readTrimmed(driver.store, base+"/name")
		if readErr != nil || name != "coretemp" {
			continue
		}
		files, listErr := driver.store.List(base)
		if listErr != nil {
			return 0, "", listErr
		}
		var tjMax int64
		var reference string
		for _, file := range files {
			if !strings.HasPrefix(file, "temp") || !strings.HasSuffix(file, "_crit") {
				continue
			}
			critical, criticalErr := readInteger(driver.store, base+"/"+file)
			if criticalErr == nil && critical > tjMax {
				tjMax = critical
				reference = base + "/" + file
			}
		}
		if tjMax > 0 {
			return tjMax / 1000, reference, nil
		}
	}
	return 0, "", fmt.Errorf("tcc: coretemp critical temperature not found")
}

type tccSnapshot struct {
	Offset int64 `json:"offset"`
}

type tccOperation struct {
	store        sysfs.Store
	coolingPath  string
	pciPath      string
	tjMax        int64
	maxOffset    int64
	requestedTCC float64
}

func (operation *tccOperation) ControlID() tuning.ControlID { return tuning.ControlThermalLimit }
func (operation *tccOperation) DriverID() string            { return "intel.tcc" }
func (operation *tccOperation) Order() int                  { return tuning.OrderThermal }

func (operation *tccOperation) Remaining(ctx context.Context) (tuning.Value, error) {
	if err := ctx.Err(); err != nil {
		return tuning.Value{}, err
	}
	pci, err := readInteger(operation.store, operation.pciPath)
	if err != nil {
		return tuning.Value{}, err
	}
	cooling, err := readInteger(operation.store, operation.coolingPath)
	if err != nil {
		return tuning.Value{}, err
	}
	if pci != cooling {
		return tuning.Value{}, fmt.Errorf("tcc: remaining interfaces disagree")
	}
	return tuning.NumericValue(float64(operation.tjMax - pci)), nil
}

func (operation *tccOperation) Capture(ctx context.Context) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	offset, err := readInteger(operation.store, operation.pciPath)
	if err != nil {
		return nil, err
	}
	return json.Marshal(tccSnapshot{Offset: offset})
}

func (operation *tccOperation) Apply(ctx context.Context) (tuning.Value, error) {
	if err := ctx.Err(); err != nil {
		return tuning.Value{}, err
	}
	offset := operation.tjMax - int64(math.Round(operation.requestedTCC))
	if offset < 0 || offset > operation.maxOffset {
		return tuning.Value{}, fmt.Errorf("tcc: requested ceiling is outside the kernel range")
	}
	return operation.writeOffset(offset)
}

func (operation *tccOperation) Restore(ctx context.Context, raw json.RawMessage) (tuning.Value, error) {
	if err := ctx.Err(); err != nil {
		return tuning.Value{}, err
	}
	var snapshot tccSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return tuning.Value{}, err
	}
	return operation.writeOffset(snapshot.Offset)
}

func (operation *tccOperation) writeOffset(offset int64) (tuning.Value, error) {
	if err := operation.store.Write(operation.pciPath, []byte(strconv.FormatInt(offset, 10))); err != nil {
		return tuning.Value{}, err
	}
	pciOffset, err := readInteger(operation.store, operation.pciPath)
	if err != nil {
		return tuning.Value{}, err
	}
	coolingOffset, err := readInteger(operation.store, operation.coolingPath)
	if err != nil {
		return tuning.Value{}, err
	}
	if pciOffset != offset || coolingOffset != offset {
		return tuning.Value{}, fmt.Errorf("tcc: read-back mismatch: requested %d, PCI %d, cooling %d", offset, pciOffset, coolingOffset)
	}
	return tuning.NumericValue(float64(operation.tjMax - offset)), nil
}

func readInteger(store sysfs.Store, path string) (int64, error) {
	value, err := readTrimmed(store, path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(value, 10, 64)
}

func readTrimmed(store sysfs.Store, path string) (string, error) {
	raw, err := store.Read(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}
