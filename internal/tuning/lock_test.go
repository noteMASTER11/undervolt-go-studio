package tuning

import "testing"

func TestRecoveryLockExcludesAnotherOwnerUntilClose(t *testing.T) {
	store := FileRecoveryStore{Directory: t.TempDir() + "/session"}
	first, err := store.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := store.Lock(); err == nil {
		second.Close()
		t.Fatal("second helper acquired live recovery lock")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := store.Lock()
	if err != nil {
		t.Fatalf("lock not released: %v", err)
	}
	third.Close()
}
