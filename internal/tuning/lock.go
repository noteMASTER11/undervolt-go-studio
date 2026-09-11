package tuning

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Lock excludes every helper and mutating harness process from discovery,
// recovery and writes until final restoration. Never unlink the lock file:
// waiters must always contend on the same inode.
func (store FileRecoveryStore) Lock() (*os.File, error) {
	directory := filepath.Dir(store.Path())
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		return nil, fmt.Errorf("recovery: insecure lock directory")
	}
	fd, err := unix.Open(filepath.Join(directory, "session.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "session.lock")
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("another tuning session owns recovery: %w", err)
	}
	return file, nil
}
