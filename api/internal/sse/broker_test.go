package sse

import (
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func recv(t *testing.T, ch <-chan Message) Message {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a message")
		return Message{}
	}
}

func TestPublishReachesSubscriber(t *testing.T) {
	b := NewBroker()
	ch, cancel := b.Subscribe("ev1")
	defer cancel()

	b.Publish("ev1", Message{Name: EventResponseSubmitted})

	if got := recv(t, ch); got.Name != EventResponseSubmitted {
		t.Fatalf("got %q, want %q", got.Name, EventResponseSubmitted)
	}
}

// TestBroadcastReachesEveryone is the fan-out the live heatmap depends on.
func TestBroadcastReachesEveryone(t *testing.T) {
	const n = 25
	b := NewBroker()

	chans := make([]<-chan Message, n)
	for i := range n {
		ch, cancel := b.Subscribe("ev1")
		defer cancel()
		chans[i] = ch
	}

	b.Publish("ev1", Message{Name: EventDecided})

	for i, ch := range chans {
		if got := recv(t, ch); got.Name != EventDecided {
			t.Fatalf("subscriber %d got %q", i, got.Name)
		}
	}
}

// TestPublishIsScopedToItsEvent: two events sharing a process must not see each
// other's traffic.
func TestPublishIsScopedToItsEvent(t *testing.T) {
	b := NewBroker()
	a, cancelA := b.Subscribe("ev1")
	defer cancelA()
	other, cancelB := b.Subscribe("ev2")
	defer cancelB()

	b.Publish("ev1", Message{Name: EventDecided})

	recv(t, a)
	select {
	case m := <-other:
		t.Fatalf("ev2 received ev1's traffic: %+v", m)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestCancelUnregisters is the leak check at the data-structure level: after
// everyone leaves, the broker must hold nothing.
func TestCancelUnregisters(t *testing.T) {
	b := NewBroker()

	_, c1 := b.Subscribe("ev1")
	_, c2 := b.Subscribe("ev1")
	if got := b.Subscribers("ev1"); got != 2 {
		t.Fatalf("Subscribers = %d, want 2", got)
	}

	c1()
	if got := b.Subscribers("ev1"); got != 1 {
		t.Fatalf("Subscribers after one cancel = %d, want 1", got)
	}

	c2()
	if got := b.Subscribers("ev1"); got != 0 {
		t.Fatalf("Subscribers after both cancel = %d, want 0", got)
	}
	// The per-event map must go too, or the outer map becomes a permanent
	// record of every event the process has ever served.
	if got := b.Events(); got != 0 {
		t.Fatalf("Events = %d, want 0; the empty event map was not cleaned up", got)
	}
}

func TestCancelIsIdempotent(t *testing.T) {
	b := NewBroker()
	_, cancel := b.Subscribe("ev1")

	cancel()
	cancel() // must not panic on a second close
}

// TestPublishDoesNotBlockOnASlowSubscriber is the property that keeps one
// stalled phone from hanging the request of whoever just submitted.
func TestPublishDoesNotBlockOnASlowSubscriber(t *testing.T) {
	b := NewBroker()
	_, cancel := b.Subscribe("ev1") // never read from
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range subscriberBuffer * 4 {
			b.Publish("ev1", Message{Name: EventResponseSubmitted})
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a subscriber that stopped reading")
	}
}

// TestNoGoroutineLeak is the phase 5 check the build plan calls out by name.
// Every connection spawns work that must clean up when the client disconnects.
func TestNoGoroutineLeak(t *testing.T) {
	b := NewBroker()

	settle := func() {
		for range 20 {
			runtime.GC()
			time.Sleep(5 * time.Millisecond)
		}
	}

	settle()
	before := runtime.NumGoroutine()

	// disconnect stands in for the request context being cancelled, which is
	// the only way an SSE handler ever ends.
	disconnect := make(chan struct{})

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()

			ch, cancel := b.Subscribe("ev1")
			defer cancel()

			for {
				select {
				case <-disconnect:
					return
				case <-ch:
				}
			}
		}()
	}

	for b.Subscribers("ev1") < 50 {
		time.Sleep(time.Millisecond)
	}
	for range 10 {
		b.Publish("ev1", Message{Name: EventResponseSubmitted})
	}

	close(disconnect)
	wg.Wait()
	settle()

	if after := runtime.NumGoroutine(); after > before+2 {
		t.Fatalf("goroutines %d -> %d; connections are not cleaning up", before, after)
	}
	if b.Events() != 0 {
		t.Fatalf("broker still holds %d events", b.Events())
	}
}

// TestConcurrentSubscribeAndPublish is what the race detector needs to see.
func TestConcurrentSubscribeAndPublish(t *testing.T) {
	b := NewBroker()
	var wg sync.WaitGroup

	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch, cancel := b.Subscribe("ev1")
			defer cancel()
			select {
			case <-ch:
			case <-time.After(20 * time.Millisecond):
			}
		}()
	}
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.Publish("ev1", Message{Name: EventResponseSubmitted})
		}()
	}

	wg.Wait()
}

// --- wire format --------------------------------------------------------------

func TestEncodeShape(t *testing.T) {
	got := Encode(Message{Name: EventDecided, Data: `{"slug":"abc"}`})
	want := "event: decided\ndata: {\"slug\":\"abc\"}\n\n"
	if got != want {
		t.Fatalf("Encode = %q, want %q", got, want)
	}
}

// TestEncodeRePrefixesEveryLine is the bug that appears the first time a
// payload contains a newline: without a data: on each line the record ends
// early and the client sees garbage.
func TestEncodeRePrefixesEveryLine(t *testing.T) {
	got := Encode(Message{Name: "x", Data: "one\ntwo"})

	if !strings.Contains(got, "data: one\ndata: two\n") {
		t.Fatalf("multi-line payload not re-prefixed: %q", got)
	}
	if !strings.HasSuffix(got, "\n\n") {
		t.Fatalf("record must end with a blank line: %q", got)
	}
}

func TestEncodeAlwaysHasData(t *testing.T) {
	// EventSource ignores a record with no data field, so an empty payload
	// still has to carry something.
	if got := Encode(Message{Name: "ping"}); !strings.Contains(got, "data: {}") {
		t.Fatalf("empty payload must still emit a data line: %q", got)
	}
}

func TestCommentAndRetry(t *testing.T) {
	if got := Comment("ping"); got != ": ping\n\n" {
		t.Fatalf("Comment = %q", got)
	}
	if got := Retry(3000); got != "retry: 3000\n\n" {
		t.Fatalf("Retry = %q", got)
	}
}
