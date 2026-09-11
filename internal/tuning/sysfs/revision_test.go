package sysfs_test

import (
	"testing"

	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/sysfs"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/testkit"
)

type movingSource struct {
	*testkit.MemoryStore
	target string
}

func (store movingSource) Resolve(path string) (string, error) {
	if path == "control" {
		return store.target, nil
	}
	return store.MemoryStore.Resolve(path)
}

func TestRevisionChangesForSameValueSourceReplacementAndFirmware(t *testing.T) {
	store := movingSource{MemoryStore: testkit.NewMemoryStore(map[string]string{"control": "44", "sys/devices/system/cpu/cpu0/microcode/version": "0x122"}), target: "source-a"}
	first := sysfs.Revision(store, "control")
	store.target = "source-b"
	second := sysfs.Revision(store, "control")
	if first == second {
		t.Fatal("same-valued source replacement kept review generation")
	}
	store.Files["sys/devices/system/cpu/cpu0/microcode/version"] = []byte("0x123")
	if second == sysfs.Revision(store, "control") {
		t.Fatal("firmware revision change kept review generation")
	}
}
