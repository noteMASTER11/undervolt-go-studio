package thermal

import (
	"context"
	"strconv"
	"testing"

	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/testkit"
)

func TestTCCCapabilityUsesEffectiveCeiling(t *testing.T) {
	store := tccStore(105, 10, 127)
	caps, err := New(store).Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(caps) != 1 {
		t.Fatalf("capabilities = %#v", caps)
	}
	capability := caps[0]
	if capability.Current.Number != 95 || capability.Range == nil || capability.Range.Maximum != 105 {
		t.Fatalf("capability = %+v", capability)
	}
	if capability.Range.Minimum != 0 {
		t.Fatalf("minimum ceiling = %v", capability.Range.Minimum)
	}
}

func TestTCCWritesOffsetAndReadsBackCeiling(t *testing.T) {
	store := &mirroredTCCStore{MemoryStore: tccStore(105, 10, 127)}
	driver := New(store)
	if _, err := driver.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	op, err := driver.Prepare(context.Background(), tuning.Change{
		ID:        tuning.ControlThermalLimit,
		Requested: tuning.NumericValue(90),
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := op.Capture(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	effective, err := op.Apply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if effective.Number != 90 {
		t.Fatalf("effective = %+v", effective)
	}
	assertRaw(t, store.MemoryStore, pciTCCPath, "15")
	assertRaw(t, store.MemoryStore, coolingRoot+"/cooling_device31/cur_state", "15")
	if _, err := op.Restore(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	assertRaw(t, store.MemoryStore, pciTCCPath, "10")
}

func TestTCCReportsReadOnlyWhenKernelInterfacesDisagree(t *testing.T) {
	store := tccStore(105, 10, 127)
	if err := store.Write(pciTCCPath, []byte("8")); err != nil {
		t.Fatal(err)
	}
	caps, err := New(store).Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if caps[0].State != tuning.StateReadOnly || caps[0].ReasonCode != "kernel_interface_mismatch" {
		t.Fatalf("capability = %+v", caps[0])
	}
}

type mirroredTCCStore struct {
	*testkit.MemoryStore
}

func (store *mirroredTCCStore) Write(path string, value []byte) error {
	if err := store.MemoryStore.Write(path, value); err != nil {
		return err
	}
	if path == pciTCCPath {
		return store.MemoryStore.Write(coolingRoot+"/cooling_device31/cur_state", value)
	}
	return nil
}

func tccStore(tjMaxCelsius, offset, maxOffset int) *testkit.MemoryStore {
	return testkit.NewMemoryStore(map[string]string{
		coolingRoot + "/cooling_device31/type":      "TCC Offset\n",
		coolingRoot + "/cooling_device31/max_state": strconv.Itoa(maxOffset) + "\n",
		coolingRoot + "/cooling_device31/cur_state": strconv.Itoa(offset) + "\n",
		pciTCCPath:                        strconv.Itoa(offset) + "\n",
		hwmonRoot + "/hwmon7/name":        "coretemp\n",
		hwmonRoot + "/hwmon7/temp1_label": "Package id 0\n",
		hwmonRoot + "/hwmon7/temp1_crit":  strconv.Itoa(tjMaxCelsius*1000) + "\n",
	})
}

func assertRaw(t *testing.T, store *testkit.MemoryStore, path, want string) {
	t.Helper()
	raw, err := store.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != want {
		t.Fatalf("%s = %q, want %q", path, raw, want)
	}
}
