package tuning

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"
)

type Engine struct {
	Capabilities CapabilitySet
	Drivers      map[string]Driver
	Recovery     RecoveryStore
	BootID       string
}

func NewEngine(capabilities CapabilitySet, recovery RecoveryStore, drivers ...Driver) *Engine {
	byID := make(map[string]Driver, len(drivers))
	for _, driver := range drivers {
		byID[driver.ID()] = driver
	}
	return &Engine{Capabilities: capabilities, Drivers: byID, Recovery: recovery}
}

type transactionOperation struct {
	operation PreparedOperation
	entry     RecoveryEntry
}

type ActiveTransaction struct {
	engine     *Engine
	record     RecoveryRecord
	operations []transactionOperation

	mu       sync.Mutex
	finished bool
}

func (engine *Engine) Apply(ctx context.Context, changes ChangeSet) (*ActiveTransaction, ValidationResult, error) {
	validation, err := ValidateChangeSet(engine.Capabilities, changes)
	if err != nil {
		return nil, ValidationResult{}, err
	}
	capabilities := make(map[ControlID]Capability, len(engine.Capabilities.Capabilities))
	for _, capability := range engine.Capabilities.Capabilities {
		capabilities[capability.ID] = capability
	}
	operations := make([]transactionOperation, 0, len(validation.ChangeSet.Changes))
	for _, change := range validation.ChangeSet.Changes {
		capability, ok := capabilities[change.ID]
		if !ok {
			return nil, validation, fmt.Errorf("transaction: capability %s disappeared", change.ID)
		}
		driver, ok := engine.Drivers[capability.DriverID]
		if !ok {
			return nil, validation, fmt.Errorf("transaction: driver %q is unavailable", capability.DriverID)
		}
		operation, err := driver.Prepare(ctx, change)
		if err != nil {
			return nil, validation, err
		}
		operations = append(operations, transactionOperation{operation: operation, entry: RecoveryEntry{DriverID: driver.ID(), ControlID: change.ID}})
	}
	sort.SliceStable(operations, func(i, j int) bool { return operations[i].operation.Order() < operations[j].operation.Order() })

	for index := range operations {
		snapshot, err := operations[index].operation.Capture(ctx)
		if err != nil {
			return nil, validation, fmt.Errorf("transaction: capture %s: %w", operations[index].entry.ControlID, err)
		}
		operations[index].entry.Snapshot = append([]byte(nil), snapshot...)
	}
	record := RecoveryRecord{
		Protocol:      RecoveryProtocol,
		MachineID:     engine.Capabilities.MachineID,
		BootID:        engine.BootID,
		TransactionID: newTransactionID(),
		State:         "applying",
		CreatedAt:     time.Now(),
		Entries:       entriesFromOperations(operations),
	}
	active := &ActiveTransaction{engine: engine, record: record, operations: operations}
	if err := engine.Recovery.Save(record); err != nil {
		return nil, validation, fmt.Errorf("transaction: save recovery record: %w", err)
	}
	for index := range active.operations {
		if _, err := active.operations[index].operation.Apply(ctx); err != nil {
			rollbackErr := active.rollbackUnlocked(context.WithoutCancel(ctx))
			return nil, validation, errors.Join(fmt.Errorf("transaction: apply %s: %w", active.operations[index].entry.ControlID, err), rollbackErr)
		}
		active.operations[index].entry.Applied = true
		active.record.Entries[index].Applied = true
		if err := engine.Recovery.Save(active.record); err != nil {
			rollbackErr := active.rollbackUnlocked(context.WithoutCancel(ctx))
			return nil, validation, errors.Join(fmt.Errorf("transaction: persist applied state: %w", err), rollbackErr)
		}
	}
	active.record.State = "active"
	if err := engine.Recovery.Save(active.record); err != nil {
		rollbackErr := active.rollbackUnlocked(context.WithoutCancel(ctx))
		return nil, validation, errors.Join(fmt.Errorf("transaction: persist active state: %w", err), rollbackErr)
	}
	return active, validation, nil
}

func (active *ActiveTransaction) Rollback(ctx context.Context) error {
	active.mu.Lock()
	defer active.mu.Unlock()
	return active.rollbackUnlocked(ctx)
}

func (active *ActiveTransaction) rollbackUnlocked(ctx context.Context) error {
	if active.finished {
		return nil
	}
	active.record.State = "rolling_back"
	_ = active.engine.Recovery.Save(active.record)
	var rollbackErr error
	for index := len(active.operations) - 1; index >= 0; index-- {
		operation := &active.operations[index]
		if !operation.entry.Applied {
			continue
		}
		if _, err := operation.operation.Restore(ctx, operation.entry.Snapshot); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore %s: %w", operation.entry.ControlID, err))
			continue
		}
		operation.entry.Applied = false
		active.record.Entries[index].Applied = false
		if err := active.engine.Recovery.Save(active.record); err != nil {
			rollbackErr = errors.Join(rollbackErr, err)
		}
	}
	if rollbackErr != nil {
		active.record.State = "rollback_incomplete"
		_ = active.engine.Recovery.Save(active.record)
		return rollbackErr
	}
	if err := active.engine.Recovery.Remove(); err != nil {
		return err
	}
	active.finished = true
	return nil
}

type RecoveryResult struct {
	Restored      []ControlID
	Discrepancies []string
	StaleBoot     bool
}

func (engine *Engine) Recover(ctx context.Context) (RecoveryResult, error) {
	record, err := engine.Recovery.Load()
	if errors.Is(err, os.ErrNotExist) {
		return RecoveryResult{}, nil
	}
	if err != nil {
		return RecoveryResult{}, err
	}
	if record.Protocol != RecoveryProtocol || record.MachineID != engine.Capabilities.MachineID {
		return RecoveryResult{}, fmt.Errorf("recovery: record does not match this machine")
	}
	if record.BootID != engine.BootID {
		result := RecoveryResult{StaleBoot: true}
		for _, entry := range record.Entries {
			if entry.Applied {
				result.Discrepancies = append(result.Discrepancies, fmt.Sprintf("%s requires a post-reboot audit", entry.ControlID))
			}
		}
		return result, nil
	}
	result := RecoveryResult{}
	var recoveryErr error
	for index := len(record.Entries) - 1; index >= 0; index-- {
		entry := &record.Entries[index]
		if !entry.Applied {
			continue
		}
		driver, ok := engine.Drivers[entry.DriverID]
		if !ok {
			recoveryErr = errors.Join(recoveryErr, fmt.Errorf("recovery: driver %s unavailable", entry.DriverID))
			continue
		}
		if _, err := driver.Restore(ctx, entry.ControlID, entry.Snapshot); err != nil {
			recoveryErr = errors.Join(recoveryErr, err)
			continue
		}
		entry.Applied = false
		result.Restored = append(result.Restored, entry.ControlID)
		if err := engine.Recovery.Save(record); err != nil {
			recoveryErr = errors.Join(recoveryErr, err)
		}
	}
	if recoveryErr != nil {
		return result, recoveryErr
	}
	return result, engine.Recovery.Remove()
}

func entriesFromOperations(operations []transactionOperation) []RecoveryEntry {
	entries := make([]RecoveryEntry, len(operations))
	for index := range operations {
		entries[index] = operations[index].entry
	}
	return entries
}

func newTransactionID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return fmt.Sprintf("tx-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(value[:])
}
