package proc

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/s1n7ax/remote-agent/internal/agent"
	"github.com/s1n7ax/remote-agent/internal/domain"
)

// Emitter is a session's event stream. Emit never blocks: events queue in
// memory and a pump delivers them in order, so a consumer that calls Send
// while handling an event cannot deadlock the session.
type Emitter struct {
	ch chan agent.Event

	mu       sync.Mutex
	cond     *sync.Cond
	queue    []agent.Event
	finished bool
}

// NewEmitter starts an emitter's pump.
func NewEmitter() *Emitter {
	e := &Emitter{ch: make(chan agent.Event, 64)}
	e.cond = sync.NewCond(&e.mu)
	go e.pump()
	return e
}

func (e *Emitter) pump() {
	for {
		e.mu.Lock()
		for len(e.queue) == 0 {
			e.cond.Wait()
		}
		ev := e.queue[0]
		e.queue[0] = agent.Event{}
		e.queue = e.queue[1:]
		e.mu.Unlock()

		e.ch <- ev
		if ev.Done {
			close(e.ch)
			return
		}
	}
}

// C is the channel handed out as Session.Events.
func (e *Emitter) C() <-chan agent.Event { return e.ch }

// Emit queues ev; it reports false once the stream has finished.
func (e *Emitter) Emit(ev agent.Event) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.finished {
		return false
	}
	ev.Done = false
	e.queue = append(e.queue, ev)
	e.cond.Signal()
	return true
}

// Log queues a log line. data, when non-nil, is stored as JSON.
func (e *Emitter) Log(kind domain.EventKind, text string, data any) bool {
	return e.Emit(agent.Event{Log: NewLog(kind, text, data)})
}

// Finish queues ev as the final event (Done set) and ends the stream; later
// calls are ignored.
func (e *Emitter) Finish(ev agent.Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.finished {
		return
	}
	e.finished = true
	ev.Done = true
	e.queue = append(e.queue, ev)
	e.cond.Signal()
}

// NewLog builds a run event stamped now.
func NewLog(kind domain.EventKind, text string, data any) *domain.RunEvent {
	ev := &domain.RunEvent{Kind: kind, Text: text, CreatedAt: time.Now().UTC()}
	switch d := data.(type) {
	case nil:
	case json.RawMessage:
		ev.Data = d
	default:
		if b, err := json.Marshal(d); err == nil {
			ev.Data = b
		}
	}
	return ev
}

// StderrSink logs stderr lines as error events, rate limited by l.
func StderrSink(e *Emitter, l *Limiter) func([]byte) {
	return func(line []byte) {
		if l.Allow() {
			e.Log(domain.EventError, Truncate(string(line), MaxToolOutput), nil)
		}
	}
}

// LogDropped notes the stderr lines l refused, if any.
func LogDropped(e *Emitter, l *Limiter) {
	if n := l.Dropped(); n > 0 {
		e.Log(domain.EventSystem, fmt.Sprintf("%d stderr lines dropped", n), nil)
	}
}
