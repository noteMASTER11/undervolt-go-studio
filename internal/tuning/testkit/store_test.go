package testkit

import (
	"errors"
	"reflect"
	"testing"
)

func TestMemoryStoreCopiesDataAndRecordsWrites(t *testing.T) {
	store := NewMemoryStore(map[string]string{"root/b": "2", "root/a": "1"})

	children, err := store.List("root")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(children, []string{"a", "b"}) {
		t.Fatalf("children = %#v", children)
	}
	if err := store.Write("root/a", []byte("3")); err != nil {
		t.Fatal(err)
	}
	if got := string(store.Files["root/a"]); got != "3" {
		t.Fatalf("stored value = %q", got)
	}
	if !reflect.DeepEqual(store.Writes, []StoreWrite{{Path: "root/a", Value: []byte("3")}}) {
		t.Fatalf("writes = %#v", store.Writes)
	}
}

func TestMemoryStoreInjectsOperationFailure(t *testing.T) {
	store := NewMemoryStore(map[string]string{"value": "1"})
	want := errors.New("blocked")
	store.Fail("read", "value", want)

	if _, err := store.Read("value"); !errors.Is(err, want) {
		t.Fatalf("err = %v", err)
	}
}
