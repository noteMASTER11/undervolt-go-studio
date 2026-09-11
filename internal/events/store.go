package events

import (
	"fmt"
	"sync"
	"time"

	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
)

const defaultCapacity = 2000

type Store struct {
	mu          sync.RWMutex
	capacity    int
	events      []tuning.Event
	subscribers map[uint64]chan tuning.Event
	nextID      uint64
}

func NewStore(capacity int) *Store {
	if capacity <= 0 {
		capacity = defaultCapacity
	}
	return &Store{capacity: capacity, subscribers: make(map[uint64]chan tuning.Event)}
}

func (store *Store) Append(event tuning.Event) {
	event = cloneEvent(event)
	if event.Time.IsZero() {
		event.Time = time.Now()
	}
	store.mu.Lock()
	if len(store.events) == store.capacity {
		copy(store.events, store.events[1:])
		store.events[len(store.events)-1] = event
	} else {
		store.events = append(store.events, event)
	}
	for _, updates := range store.subscribers {
		select {
		case updates <- event:
		default:
			select {
			case <-updates:
			default:
			}
			select {
			case updates <- event:
			default:
			}
		}
	}
	store.mu.Unlock()
}

func (store *Store) Snapshot() []tuning.Event {
	store.mu.RLock()
	defer store.mu.RUnlock()
	result := make([]tuning.Event, len(store.events))
	for index, event := range store.events {
		result[index] = cloneEvent(event)
	}
	return result
}

func (store *Store) Subscribe() (<-chan tuning.Event, func()) {
	store.mu.Lock()
	id := store.nextID
	store.nextID++
	updates := make(chan tuning.Event, 1)
	store.subscribers[id] = updates
	store.mu.Unlock()
	var once sync.Once
	return updates, func() {
		once.Do(func() {
			store.mu.Lock()
			delete(store.subscribers, id)
			close(updates)
			store.mu.Unlock()
		})
	}
}

func FromError(id tuning.ControlID, err error) tuning.Event {
	label := controlLabel(id)
	return tuning.Event{
		Time:      time.Now(),
		Kind:      "error",
		Message:   fmt.Sprintf("Could not apply %s", label),
		Detail:    err.Error(),
		ControlID: id,
	}
}

func controlLabel(id tuning.ControlID) string {
	switch id {
	case tuning.ControlPL1:
		return "sustained CPU power limit"
	case tuning.ControlPL2:
		return "short boost CPU power limit"
	case tuning.ControlTau:
		return "turbo time window"
	case tuning.ControlEPP:
		return "energy preference"
	case tuning.ControlThermalLimit:
		return "CPU thermal limit"
	case tuning.ControlRatioPCore:
		return "P-core ratios"
	case tuning.ControlRatioECore:
		return "E-core ratios"
	case tuning.ControlVoltageCore:
		return "Core voltage offset"
	case tuning.ControlVoltageCache:
		return "Cache voltage offset"
	default:
		return "tuning control"
	}
}

func cloneEvent(event tuning.Event) tuning.Event {
	cloned := event
	if event.Capabilities != nil {
		capabilities := *event.Capabilities
		capabilities.Capabilities = append([]tuning.Capability(nil), event.Capabilities.Capabilities...)
		cloned.Capabilities = &capabilities
	}
	cloned.Effective = cloneValues(event.Effective)
	cloned.Remaining = cloneValues(event.Remaining)
	return cloned
}

func cloneValues(values map[tuning.ControlID]tuning.Value) map[tuning.ControlID]tuning.Value {
	if values == nil {
		return nil
	}
	cloned := make(map[tuning.ControlID]tuning.Value, len(values))
	for id, value := range values {
		value.Vector = append([]float64(nil), value.Vector...)
		cloned[id] = value
	}
	return cloned
}
