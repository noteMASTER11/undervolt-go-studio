package sysfs

import (
	"crypto/sha256"
	"fmt"
	"sort"
)

// Revision fingerprints backend-owned source identities and firmware state.
// Only the digest contributes to the wire generation; paths never cross it.
func Revision(store Store, paths ...string) string {
	paths = append(append([]string(nil), paths...), "sys/devices/system/cpu/cpu0/microcode/version", "sys/devices/system/cpu/online", "sys/devices/system/cpu/intel_pstate/status")
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		resolved, resolveErr := store.Resolve(path)
		raw, readErr := store.Read(path)
		fmt.Fprintf(hash, "%q\x00%q\x00%q\x00%t\x00%t\n", path, resolved, raw, resolveErr == nil, readErr == nil)
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}
