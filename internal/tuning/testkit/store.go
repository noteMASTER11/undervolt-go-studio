package testkit

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
)

type StoreWrite struct {
	Path  string
	Value []byte
}

type MemoryStore struct {
	mu       sync.RWMutex
	Files    map[string][]byte
	Writes   []StoreWrite
	Failures map[string]error
}

func NewMemoryStore(files map[string]string) *MemoryStore {
	store := &MemoryStore{
		Files:    make(map[string][]byte, len(files)),
		Failures: make(map[string]error),
	}
	for name, value := range files {
		store.Files[normalize(name)] = []byte(value)
	}
	return store
}

func (store *MemoryStore) Fail(operation, name string, err error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.Failures[failureKey(operation, name)] = err
}

func (store *MemoryStore) Read(name string) ([]byte, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	name = normalize(name)
	if err := store.failure("read", name); err != nil {
		return nil, err
	}
	value, ok := store.Files[name]
	if !ok {
		return nil, fmt.Errorf("testkit: file not found: %s", name)
	}
	return append([]byte(nil), value...), nil
}

func (store *MemoryStore) Write(name string, value []byte) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	name = normalize(name)
	if err := store.failure("write", name); err != nil {
		return err
	}
	copyOfValue := append([]byte(nil), value...)
	store.Files[name] = copyOfValue
	store.Writes = append(store.Writes, StoreWrite{Path: name, Value: append([]byte(nil), value...)})
	return nil
}

func (store *MemoryStore) List(name string) ([]string, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	name = normalize(name)
	if err := store.failure("list", name); err != nil {
		return nil, err
	}
	prefix := ""
	if name != "." {
		prefix = name + "/"
	}
	unique := make(map[string]struct{})
	for file := range store.Files {
		if !strings.HasPrefix(file, prefix) {
			continue
		}
		rest := strings.TrimPrefix(file, prefix)
		if rest == "" {
			continue
		}
		child := strings.SplitN(rest, "/", 2)[0]
		unique[child] = struct{}{}
	}
	children := make([]string, 0, len(unique))
	for child := range unique {
		children = append(children, child)
	}
	sort.Strings(children)
	return children, nil
}

func (store *MemoryStore) Resolve(name string) (string, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	name = normalize(name)
	if err := store.failure("resolve", name); err != nil {
		return "", err
	}
	return name, nil
}

func (store *MemoryStore) failure(operation, name string) error {
	return store.Failures[failureKey(operation, name)]
}

func failureKey(operation, name string) string {
	return strings.ToLower(operation) + ":" + normalize(name)
}

func normalize(name string) string {
	return strings.TrimPrefix(path.Clean(strings.ReplaceAll(name, "\\", "/")), "/")
}
