package sysfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Store interface {
	Read(path string) ([]byte, error)
	Write(path string, value []byte) error
	List(path string) ([]string, error)
	Resolve(path string) (string, error)
}

type RootStore struct {
	Root string
}

func (store RootStore) Read(path string) ([]byte, error) {
	resolved, err := store.Resolve(path)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(resolved)
}

func (store RootStore) Write(path string, value []byte) error {
	resolved, err := store.Resolve(path)
	if err != nil {
		return err
	}
	return os.WriteFile(resolved, value, 0)
}

func (store RootStore) List(path string) ([]string, error) {
	resolved, err := store.Resolve(path)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return nil, err
	}
	children := make([]string, 0, len(entries))
	for _, entry := range entries {
		children = append(children, entry.Name())
	}
	return children, nil
}

func (store RootStore) Resolve(path string) (string, error) {
	if filepath.IsAbs(filepath.FromSlash(path)) {
		return "", fmt.Errorf("sysfs: absolute path is not allowed: %q", path)
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("sysfs: path escapes root: %q", path)
	}

	root := store.Root
	if root == "" {
		root = string(filepath.Separator)
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("sysfs: resolve root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("sysfs: resolve root symlinks: %w", err)
	}
	candidate, err := filepath.EvalSymlinks(filepath.Join(root, clean))
	if err != nil {
		return "", fmt.Errorf("sysfs: resolve %q: %w", path, err)
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("sysfs: resolved path escapes root: %q", candidate)
	}
	return candidate, nil
}
