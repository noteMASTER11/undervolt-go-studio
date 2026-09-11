package events

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
)

func TestStoreKeepsNewestEventsWithinCapacity(t *testing.T) {
	store := NewStore(2)
	store.Append(event("one"))
	store.Append(event("two"))
	store.Append(event("three"))
	got := store.Snapshot()
	if len(got) != 2 || got[0].Message != "two" || got[1].Message != "three" {
		t.Fatalf("got = %v", got)
	}
}

func TestUserMessageDoesNotContainRawRegister(t *testing.T) {
	event := FromError(tuning.ControlPL1, errors.New("write msr 0x610: EIO"))
	if strings.Contains(event.Message, "0x610") || !strings.Contains(event.Detail, "0x610") {
		t.Fatalf("event = %+v", event)
	}
}

func TestSubscriberReceivesLatestWithoutBlockingProducer(t *testing.T) {
	store := NewStore(4)
	updates, cancel := store.Subscribe()
	defer cancel()
	store.Append(event("one"))
	store.Append(event("two"))
	select {
	case got := <-updates:
		if got.Message != "two" {
			t.Fatalf("got = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no update received")
	}
}

func event(message string) tuning.Event {
	return tuning.Event{Time: time.Now(), Kind: "info", Message: message}
}
