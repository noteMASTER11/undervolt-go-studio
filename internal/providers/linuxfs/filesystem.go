package linuxfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type FileSystem interface {
	ReadFile(path string) ([]byte, error)
	ReadDir(path string) ([]os.DirEntry, error)
	RealPath(path string) (string, error)
}

// RootFS confines provider paths beneath Root.
type RootFS struct {
	Root string
}

func (r RootFS) ReadFile(path string) ([]byte, error) {
	resolved, err := r.resolve(path)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(resolved)
}

func (r RootFS) ReadDir(path string) ([]os.DirEntry, error) {
	resolved, err := r.resolve(path)
	if err != nil {
		return nil, err
	}
	return os.ReadDir(resolved)
}

// RealPath resolves sysfs links and returns a root-relative stable native path.
func (r RootFS) RealPath(path string) (string, error) {
	resolved, err := r.resolve(path)
	if err != nil {
		return "", err
	}
	realPath, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		return "", err
	}
	root := r.Root
	if root == "" {
		root = string(filepath.Separator)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, realPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("linuxfs: resolved path escapes root: %q", realPath)
	}
	return filepath.ToSlash(relative), nil
}

func (r RootFS) resolve(path string) (string, error) {
	path = filepath.Clean(filepath.FromSlash(path))
	if filepath.IsAbs(path) || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("linuxfs: path escapes root: %q", path)
	}
	root := r.Root
	if root == "" {
		root = string(filepath.Separator)
	}
	return filepath.Join(root, path), nil
}
