package sysfs

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRootStoreRejectsEscape(t *testing.T) {
	root := t.TempDir()
	store := RootStore{Root: root}
	for _, path := range []string{"../etc/passwd", "/etc/passwd", "sys/../../etc/passwd"} {
		if _, err := store.Read(path); err == nil {
			t.Fatalf("Read(%q) succeeded", path)
		}
		if err := store.Write(path, []byte("x")); err == nil {
			t.Fatalf("Write(%q) succeeded", path)
		}
	}
}

func TestRootStoreRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "value"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escaped")); err != nil {
		t.Fatal(err)
	}
	store := RootStore{Root: root}

	if _, err := store.Read("escaped/value"); err == nil {
		t.Fatal("read through escaping symlink succeeded")
	}
	if err := store.Write("escaped/value", []byte("changed")); err == nil {
		t.Fatal("write through escaping symlink succeeded")
	}
}

func TestRootStoreReadsWritesListsAndResolvesWithinRoot(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "sys", "class")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "a"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "b"), []byte("2"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := RootStore{Root: root}

	if err := store.Write("sys/class/a", []byte("3")); err != nil {
		t.Fatal(err)
	}
	value, err := store.Read("sys/class/a")
	if err != nil {
		t.Fatal(err)
	}
	if string(value) != "3" {
		t.Fatalf("value = %q", value)
	}
	children, err := store.List("sys/class")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(children, []string{"a", "b"}) {
		t.Fatalf("children = %#v", children)
	}
	resolved, err := store.Resolve("sys/class/a")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != filepath.Join(root, "sys", "class", "a") {
		t.Fatalf("resolved = %q", resolved)
	}
}
