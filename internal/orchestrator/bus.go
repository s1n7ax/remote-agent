package orchestrator

import "sync"

// Message is one live update sent to the UI.
type Message struct {
	Type      string `json:"type"` // ticket | run_event | board
	ProjectID int64  `json:"projectId,omitempty"`
	TicketID  int64  `json:"ticketId,omitempty"`
	Data      any    `json:"data,omitempty"`
}

// Bus fans live updates out to subscribers (SSE clients). Slow subscribers
// drop messages instead of blocking the orchestrator.
type Bus struct {
	mu   sync.Mutex
	subs map[chan Message]struct{}
}

// NewBus returns an empty bus.
func NewBus() *Bus { return &Bus{subs: map[chan Message]struct{}{}} }

// Subscribe returns a channel of messages and a function that unsubscribes.
func (b *Bus) Subscribe() (<-chan Message, func()) {
	ch := make(chan Message, 64)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
}

// Publish sends m to every subscriber.
func (b *Bus) Publish(m Message) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- m:
		default:
		}
	}
}
