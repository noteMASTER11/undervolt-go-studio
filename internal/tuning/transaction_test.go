package tuning

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
)

func TestEngineAppliesSafeOrderAndRollsBackReverseOrder(t *testing.T) {
	var log []string
	drivers, capabilities := orderedTestDrivers(&log, map[ControlID]int{
		ControlPL1:          OrderPower,
		ControlThermalLimit: OrderThermal,
		ControlRatioPCore:   OrderRatio,
		ControlVoltageCore:  OrderVoltage,
	})
	store := &memoryRecoveryStore{}
	engine := NewEngine(capabilities, store, drivers...)
	active, _, err := engine.Apply(context.Background(), changesFor(capabilities))
	if err != nil {
		t.Fatal(err)
	}
	if active.ID() == "" {
		t.Fatal("active transaction has no ID")
	}
	if err := active.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"apply:intel.package.pl1", "apply:intel.package.thermal_limit", "apply:intel.ratio.pcore", "apply:intel.voltage.core",
		"restore:intel.voltage.core", "restore:intel.ratio.pcore", "restore:intel.package.thermal_limit", "restore:intel.package.pl1",
	}
	if !reflect.DeepEqual(log, want) {
		t.Fatalf("log = %v", log)
	}
	if store.present {
		t.Fatal("recovery record remained after successful rollback")
	}
}

func TestEngineRollsBackAppliedOperationsWhenLaterApplyFails(t *testing.T) {
	var log []string
	driverA := &transactionTestDriver{id: "a", order: OrderPower, log: &log}
	driverB := &transactionTestDriver{id: "b", order: OrderThermal, log: &log, applyErr: errors.New("injected apply failure")}
	capabilities := CapabilitySet{Generation: "g", MachineID: "cpu", Capabilities: []Capability{
		{ID: ControlPL1, State: StateSupported, Unit: UnitWatt, Current: NumericValue(40), Range: &NumericRange{Minimum: 1, Maximum: 55, Step: 1}, DriverID: "a"},
		{ID: ControlThermalLimit, State: StateSupported, Unit: UnitCelsius, Current: NumericValue(95), Range: &NumericRange{Minimum: 70, Maximum: 105, Step: 1}, DriverID: "b"},
	}}
	store := &memoryRecoveryStore{}
	_, _, err := NewEngine(capabilities, store, driverA, driverB).Apply(context.Background(), ChangeSet{
		Generation: "g", MachineID: "cpu", Changes: []Change{
			{ID: ControlPL1, Requested: NumericValue(35)},
			{ID: ControlThermalLimit, Requested: NumericValue(90)},
		},
	})
	if err == nil {
		t.Fatal("transaction unexpectedly succeeded")
	}
	want := []string{"apply:intel.package.pl1", "apply:intel.package.thermal_limit", "restore:intel.package.thermal_limit", "restore:intel.package.pl1"}
	if !reflect.DeepEqual(log, want) {
		t.Fatalf("log = %v", log)
	}
	if store.present {
		t.Fatal("recovery record remained after failure rollback")
	}
}

func TestRecoverRestoresSameBootRecord(t *testing.T) {
	var log []string
	driver := &transactionTestDriver{id: "driver", order: OrderPower, log: &log}
	store := &memoryRecoveryStore{present: true, record: RecoveryRecord{
		Protocol: 1, MachineID: "cpu", BootID: "boot", State: "active",
		Entries: []RecoveryEntry{{DriverID: "driver", ControlID: ControlPL1, Snapshot: json.RawMessage(`1`), Applied: true}},
	}}
	engine := NewEngine(CapabilitySet{MachineID: "cpu"}, store, driver)
	engine.BootID = "boot"
	result, err := engine.Recover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Restored) != 1 || store.present {
		t.Fatalf("result=%+v present=%v", result, store.present)
	}
}

type memoryRecoveryStore struct {
	record  RecoveryRecord
	present bool
	saveErr error
}

func (store *memoryRecoveryStore) Load() (RecoveryRecord, error) {
	if !store.present {
		return RecoveryRecord{}, os.ErrNotExist
	}
	return store.record, nil
}

func (store *memoryRecoveryStore) Save(record RecoveryRecord) error {
	if store.saveErr != nil {
		return store.saveErr
	}
	store.record, store.present = record, true
	return nil
}

func (store *memoryRecoveryStore) Remove() error {
	store.present = false
	return nil
}

type transactionTestDriver struct {
	id         string
	order      int
	log        *[]string
	applyErr   error
	onApply    func()
	restoreErr error
}

func (driver *transactionTestDriver) ID() string { return driver.id }
func (driver *transactionTestDriver) Probe(context.Context) ([]Capability, error) {
	return nil, nil
}
func (driver *transactionTestDriver) Prepare(_ context.Context, change Change) (PreparedOperation, error) {
	return &transactionTestOperation{driver: driver, change: change}, nil
}
func (driver *transactionTestDriver) Restore(_ context.Context, id ControlID, _ json.RawMessage) (Value, error) {
	*driver.log = append(*driver.log, "restore:"+string(id))
	return NumericValue(0), nil
}

type transactionTestOperation struct {
	driver *transactionTestDriver
	change Change
}

func (operation *transactionTestOperation) ControlID() ControlID { return operation.change.ID }
func (operation *transactionTestOperation) DriverID() string     { return operation.driver.id }
func (operation *transactionTestOperation) Order() int           { return operation.driver.order }
func (operation *transactionTestOperation) Capture(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`1`), nil
}
func (operation *transactionTestOperation) Apply(context.Context) (Value, error) {
	*operation.driver.log = append(*operation.driver.log, "apply:"+string(operation.change.ID))
	if operation.driver.onApply != nil {
		operation.driver.onApply()
	}
	return operation.change.Requested, operation.driver.applyErr
}
func (operation *transactionTestOperation) Restore(_ context.Context, _ json.RawMessage) (Value, error) {
	*operation.driver.log = append(*operation.driver.log, "restore:"+string(operation.change.ID))
	return NumericValue(0), operation.driver.restoreErr
}

func TestRecoveryArmedBeforeWriteAndRetainedAfterFailedVerification(t *testing.T) {
	var log []string
	drivers, caps := orderedTestDrivers(&log, map[ControlID]int{ControlPL1: OrderPower})
	store := &memoryRecoveryStore{}
	driver := drivers[0].(*transactionTestDriver)
	driver.onApply = func() {
		if !store.present || !store.record.Entries[0].Applied {
			t.Error("possibly modified state was not durable before write")
		}
	}
	driver.applyErr = errors.New("write succeeded, verification failed")
	driver.restoreErr = errors.New("restore failed")
	_, _, err := NewEngine(caps, store, drivers...).Apply(context.Background(), changesFor(caps))
	if err == nil || !store.present || !store.record.Entries[0].Applied {
		t.Fatalf("lost recovery after unverified write: error=%v present=%v record=%+v", err, store.present, store.record)
	}
}

func TestTransactionRetainsVerifiedEffectiveValues(t *testing.T) {
	var log []string
	drivers, caps := orderedTestDrivers(&log, map[ControlID]int{ControlPL1: OrderPower})
	active, _, err := NewEngine(caps, &memoryRecoveryStore{}, drivers...).Apply(context.Background(), changesFor(caps))
	if err != nil {
		t.Fatal(err)
	}
	if got := active.Effective()[ControlPL1]; got.Kind != ValueNumeric || got.Number != 30 {
		t.Fatalf("verified result lost: %+v", got)
	}
	active.Rollback(context.Background())
}

func TestFailedApplyReturnsRetryableIncompleteRollback(t *testing.T) {
	var log []string
	drivers, caps := orderedTestDrivers(&log, map[ControlID]int{ControlPL1: OrderPower})
	driver := drivers[0].(*transactionTestDriver)
	driver.applyErr = errors.New("verify")
	driver.restoreErr = errors.New("restore")
	active, _, err := NewEngine(caps, &memoryRecoveryStore{}, drivers...).Apply(context.Background(), changesFor(caps))
	var incomplete *RollbackError
	if active == nil || !errors.As(err, &incomplete) {
		t.Fatalf("lost retryable rollback: active=%v err=%v", active, err)
	}
	if len(incomplete.Unverified) != 1 || incomplete.Unverified[0] != ControlPL1 {
		t.Fatalf("unknown remaining value not identified: %+v", incomplete)
	}
	driver.restoreErr = nil
	if err := active.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func orderedTestDrivers(log *[]string, orders map[ControlID]int) ([]Driver, CapabilitySet) {
	capabilities := CapabilitySet{Generation: "g", MachineID: "cpu"}
	var drivers []Driver
	for id, order := range orders {
		driverID := string(id)
		drivers = append(drivers, &transactionTestDriver{id: driverID, order: order, log: log})
		capability := Capability{ID: id, State: StateSupported, DriverID: driverID}
		switch id {
		case ControlRatioPCore:
			capability.Unit, capability.Current = UnitRatio, VectorValue([]float64{50})
		default:
			capability.Unit, capability.Current = UnitWatt, NumericValue(40)
			capability.Range = &NumericRange{Minimum: -250, Maximum: 105, Step: 1}
		}
		capabilities.Capabilities = append(capabilities.Capabilities, capability)
	}
	return drivers, capabilities
}

func changesFor(capabilities CapabilitySet) ChangeSet {
	set := ChangeSet{Generation: capabilities.Generation, MachineID: capabilities.MachineID}
	for _, capability := range capabilities.Capabilities {
		requested := NumericValue(30)
		if capability.ID == ControlRatioPCore {
			requested = VectorValue([]float64{49})
		} else if capability.ID == ControlVoltageCore {
			requested = NumericValue(-10)
		} else if capability.ID == ControlThermalLimit {
			requested = NumericValue(90)
		}
		set.Changes = append(set.Changes, Change{ID: capability.ID, Requested: requested})
	}
	return set
}
