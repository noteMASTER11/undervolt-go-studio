package tuning

import (
	"os"
	"testing"
)

func TestRecoveryStoreWrites0600AndRoundTrips(t *testing.T) {
	store := FileRecoveryStore{Directory: t.TempDir()}
	want := RecoveryRecord{Protocol: 1, MachineID: "cpu", BootID: "boot", State: "applying"}
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	got, err := store.Load()
	if err != nil || got.MachineID != want.MachineID || got.BootID != want.BootID {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestRecoveryStoreRemoveIsIdempotent(t *testing.T) {
	store := FileRecoveryStore{Directory: t.TempDir()}
	if err := store.Remove(); err != nil {
		t.Fatal(err)
	}
}
