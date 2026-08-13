// Package sse is a small in-process publish/subscribe broker for server-sent
// events, plus the wire encoding.
//
// It is deliberately not a library. Group scheduling is asynchronous -- people
// fill a poll in on a commute, nobody watches the grid -- so one-directional
// streaming with no reconnection protocol is the honest fit, and the whole
// thing is small enough to reason about.
//
// Everything here must survive a client vanishing mid-write, which is the
// normal case rather than the exception: phones sleep, tabs close, tunnels
// drop.
package sse

import (
	"fmt"
	"strings"
	"sync"
)

// Event names the API broadcasts.
const (
	EventResponseSubmitted = "response_submitted"
	EventDecided           = "decided"
	EventReopened          = "reopened"

	// EventPing is the heartbeat. It carries no meaning beyond "this stream is
	// still alive", which is the one thing a client cannot otherwise find out.
	EventPing = "ping"
)

// Message is one broadcast. Data is deliberately tiny: subscribers refetch the
// event rather than applying a payload, so a dropped message costs one stale
// second and never leaves a client holding a wrong partial state.
type Message struct {
	Name string
	Data string
}

// subscriberBuffer is how many messages a slow client may fall behind before
// its messages start being dropped.
//
// Dropping is safe precisely because a message carries no state: the client
// refetches on any message, so collapsing three notifications into one produces
// the same end result one round trip later.
const subscriberBuffer = 8

// Broker fans messages out to the subscribers of one event each.
//
// The zero value is not usable; call NewBroker.
type Broker struct {
	mu   sync.Mutex
	subs map[string]map[chan Message]struct{}
}

func NewBroker() *Broker {
	return &Broker{subs: make(map[string]map[chan Message]struct{})}
}

// Subscribe registers interest in one event and returns the channel to read
// from together with the function that tears it down.
//
// The caller must call cancel, normally with defer. Failing to do so leaks both
// the channel and the map entry, which is the classic version of this bug: the
// handler returns when the client disconnects and nothing ever removes the
// subscriber, so the map grows for the life of the process.
func (b *Broker) Subscribe(eventID string) (<-chan Message, func()) {
	ch := make(chan Message, subscriberBuffer)

	b.mu.Lock()
	if b.subs[eventID] == nil {
		b.subs[eventID] = make(map[chan Message]struct{})
	}
	b.subs[eventID][ch] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	cancel := func() {
		// Idempotent: a handler may cancel on its own path out and again
		// through a defer, and closing a channel twice panics.
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()

			if set, ok := b.subs[eventID]; ok {
				delete(set, ch)
				if len(set) == 0 {
					// Drop the empty inner map too, or the outer map becomes a
					// permanent record of every event ever watched.
					delete(b.subs, eventID)
				}
			}
			// Closed under the same lock Publish holds, so a send can never
			// race with this and panic on a closed channel.
			close(ch)
		})
	}

	return ch, cancel
}

// Publish delivers a message to everyone watching an event.
//
// Sends are non-blocking. A subscriber that has stopped reading must not be
// able to stall the request that triggered the broadcast, and since messages
// carry no state, dropping one for a lagging client is harmless.
func (b *Broker) Publish(eventID string, m Message) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for ch := range b.subs[eventID] {
		select {
		case ch <- m:
		default:
			// Full buffer: this client is behind and will refetch on the next
			// message it does receive, or on reconnect.
		}
	}
}

// Subscribers reports how many connections are watching an event. Exported for
// tests and diagnostics.
func (b *Broker) Subscribers(eventID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs[eventID])
}

// Events reports how many events currently have at least one subscriber. A
// number that only ever grows is the signature of a leaked cancel.
func (b *Broker) Events() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}

// Encode renders a message in the text/event-stream format.
//
// Every line of the payload needs its own "data:" prefix, and the record ends
// with a blank line. A payload containing a newline that is not re-prefixed is
// the standard way to produce a stream that works until someone's name has a
// line break in it.
func Encode(m Message) string {
	var b strings.Builder
	if m.Name != "" {
		fmt.Fprintf(&b, "event: %s\n", m.Name)
	}
	data := m.Data
	if data == "" {
		data = "{}"
	}
	for _, line := range strings.Split(data, "\n") {
		fmt.Fprintf(&b, "data: %s\n", line)
	}
	b.WriteString("\n")
	return b.String()
}

// Comment renders a stream comment, used as a heartbeat. It is valid to send at
// any time and is ignored by EventSource, which makes it the right way to keep
// an idle connection from being reaped by an intermediary.
func Comment(text string) string {
	return ": " + text + "\n\n"
}

// Retry tells the browser how long to wait before reconnecting.
func Retry(ms int) string {
	return fmt.Sprintf("retry: %d\n\n", ms)
}
