package msr

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

type LinuxDevice struct {
	Root string
}

func (device LinuxDevice) Read(cpu int, register uint32) (uint64, error) {
	file, err := os.OpenFile(device.path(cpu), os.O_RDONLY, 0)
	if err != nil {
		return 0, classifyIOError(err)
	}
	defer file.Close()
	buffer := make([]byte, 8)
	if _, err := file.ReadAt(buffer, int64(register)); err != nil {
		return 0, classifyIOError(err)
	}
	return binary.LittleEndian.Uint64(buffer), nil
}

func (device LinuxDevice) Write(cpu int, register uint32, value uint64) error {
	file, err := os.OpenFile(device.path(cpu), os.O_RDWR, 0)
	if err != nil {
		return classifyIOError(err)
	}
	defer file.Close()
	buffer := make([]byte, 8)
	binary.LittleEndian.PutUint64(buffer, value)
	if _, err := file.WriteAt(buffer, int64(register)); err != nil {
		return classifyIOError(err)
	}
	return nil
}

func (device LinuxDevice) path(cpu int) string {
	root := device.Root
	if root == "" {
		root = "/dev"
	}
	if cpu < 0 {
		return filepath.Join(root, "cpu", "invalid", "msr")
	}
	return filepath.Join(root, "cpu", strconv.Itoa(cpu), "msr")
}

func classifyIOError(err error) error {
	if err == nil {
		return nil
	}
	code := ReasonIO
	switch {
	case errors.Is(err, syscall.ENOENT), errors.Is(err, fs.ErrNotExist):
		code = ReasonDeviceMissing
	case errors.Is(err, syscall.EPERM):
		code = ReasonKernelLockdown
	case errors.Is(err, syscall.EACCES), errors.Is(err, fs.ErrPermission):
		code = ReasonPermissionDenied
	case errors.Is(err, syscall.EIO):
		code = ReasonGeneralProtection
	}
	return &ReasonError{Code: code, Err: fmt.Errorf("MSR device I/O: %w", err)}
}
