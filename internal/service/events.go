package service

import (
	"encoding/json"
	"sync"
	"time"
)

type Event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
	At   string `json:"at"`
}

// EventHub is an in-process best-effort event fan-out used by the demo SSE
// endpoint. Durable state always lives in SQLite; a slow browser may miss an
// event and should refresh through REST.
type EventHub struct {
	mu          sync.Mutex
	next        uint64
	subscribers map[uint64]chan Event
}

func NewEventHub() *EventHub {
	return &EventHub{subscribers: make(map[uint64]chan Event)}
}

func (h *EventHub) Subscribe() (<-chan Event, func()) {
	h.mu.Lock()
	h.next++
	id := h.next
	channel := make(chan Event, 32)
	h.subscribers[id] = channel
	h.mu.Unlock()
	var once sync.Once
	return channel, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subscribers, id)
			close(channel)
			h.mu.Unlock()
		})
	}
}

func (h *EventHub) Publish(eventType string, data any) {
	event := Event{Type: eventType, Data: data, At: time.Now().UTC().Format(time.RFC3339Nano)}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, subscriber := range h.subscribers {
		select {
		case subscriber <- event:
		default:
			// Keep request paths non-blocking. REST remains the source of truth.
		}
	}
}

func (e Event) JSON() []byte {
	encoded, _ := json.Marshal(e.Data)
	return encoded
}
