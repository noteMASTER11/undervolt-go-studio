package tuning

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const RecoveryProtocol = 1

type RecoveryEntry struct {
	DriverID  string          `json:"driver_id"`
	ControlID ControlID       `json:"control_id"`
	Snapshot  json.RawMessage `json:"snapshot"`
	Applied   bool            `json:"applied"`
}

type RecoveryRecord struct {
	Protocol      int             `json:"protocol"`
	MachineID     string          `json:"machine_id"`
	BootID        string          `json:"boot_id"`
	TransactionID string          `json:"transaction_id"`
	State         string          `json:"state"`
	CreatedAt     time.Time       `json:"created_at"`
	Entries       []RecoveryEntry `json:"entries"`
}

type RecoveryStore interface {
	Load() (RecoveryRecord, error)
	Save(RecoveryRecord) error
	Remove() error
}

type FileRecoveryStore struct {
	Directory string
}

func (store FileRecoveryStore) Path() string {
	directory := store.Directory
	if directory == "" {
		directory = "/run/undervolt-go-studio"
	}
	return filepath.Join(directory, "recovery-v1.json")
}

func (store FileRecoveryStore) Load() (RecoveryRecord, error) {
	file, err := os.Open(store.Path())
	if err != nil {
		return RecoveryRecord{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var record RecoveryRecord
	if err := decoder.Decode(&record); err != nil {
		return RecoveryRecord{}, fmt.Errorf("recovery: decode record: %w", err)
	}
	return record, nil
}

func (store FileRecoveryStore) Save(record RecoveryRecord) error {
	directory := filepath.Dir(store.Path())
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("recovery: create directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("recovery: secure directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".recovery-v1-*")
	if err != nil {
		return fmt.Errorf("recovery: create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	keepTemporary := true
	defer func() {
		_ = temporary.Close()
		if keepTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	encoder := json.NewEncoder(temporary)
	if err := encoder.Encode(record); err != nil {
		return fmt.Errorf("recovery: encode record: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("recovery: sync record: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("recovery: close record: %w", err)
	}
	if err := os.Rename(temporaryPath, store.Path()); err != nil {
		return fmt.Errorf("recovery: publish record: %w", err)
	}
	keepTemporary = false
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("recovery: open directory for sync: %w", err)
	}
	defer directoryHandle.Close()
	if err := directoryHandle.Sync(); err != nil {
		return fmt.Errorf("recovery: sync directory: %w", err)
	}
	return nil
}

func (store FileRecoveryStore) Remove() error {
	err := os.Remove(store.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
