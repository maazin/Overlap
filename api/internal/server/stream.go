package server

import (
	"net/http"
	"time"

	"github.com/maazinshaikh/overlap/api/internal/sse"
)

const (
	// heartbeatInterval keeps idle connections alive and, just as importantly,
	// gives the client something to miss.
	//
	// Browsers cannot be trusted to notice a severed stream: a killed server
	// can leave EventSource sitting in the OPEN state with no error and no
	// reconnect, so a client that waits to be told is a client that waits
	// forever. The heartbeat is a named event rather than a bare comment
	// precisely so JavaScript can see it -- comments keep proxies happy but are
	// invisible to EventSource -- and the client treats silence as a dead
	// connection and redials.
	heartbeatInterval = 15 * time.Second

	// reconnectDelay is what the browser waits before redialling. EventSource
	// reconnects on its own; this only sets the pace.
	reconnectDelay = 3000
)

// handleStream holds a connection open and writes events as they happen.
//
// The handler owns exactly one subscription and returns on exactly one
// condition: the request context ending. That covers both a client
// disconnecting and the process shutting down, because the server's BaseContext
// is the signal-cancelled context from main, so SIGTERM cancels every in-flight
// request. Without that, a graceful shutdown would block on streams that by
// design never finish.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	ev, err := s.store.EventBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.eventLookupError(w, r, err)
		return
	}

	// Streaming needs the response written incrementally. If something in the
	// middleware chain has wrapped the writer without forwarding Flush, this is
	// where it surfaces, rather than as a stream that silently buffers.
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.log.ErrorContext(r.Context(), "response writer does not support flushing")
		s.writeError(w, r, http.StatusInternalServerError, "streaming is not available")
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	// Tells nginx-style proxies not to buffer, which would defeat the point.
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	msgs, cancel := s.broker.Subscribe(ev.ID)
	// The whole leak story is this line. Every return path below goes through
	// it, so a subscriber is never left in the broker after its reader is gone.
	defer cancel()

	write := func(chunk string) bool {
		if _, err := w.Write([]byte(chunk)); err != nil {
			// The client vanished. Normal, and not worth an error log.
			return false
		}
		flusher.Flush()
		return true
	}

	if !write(sse.Retry(reconnectDelay)) {
		return
	}
	// An immediate comment makes the connection usable straight away rather
	// than leaving the browser waiting for the first real event.
	if !write(sse.Comment("connected")) {
		return
	}

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return

		case m, open := <-msgs:
			if !open {
				return
			}
			if !write(sse.Encode(m)) {
				return
			}

		case <-ticker.C:
			if !write(sse.Encode(sse.Message{Name: sse.EventPing})) {
				return
			}
		}
	}
}

// publish broadcasts to an event's watchers.
//
// Messages carry no state on purpose: a client refetches when it hears
// anything. That makes a dropped or collapsed message cost one stale second
// instead of leaving somebody holding a half-applied update, and it means
// reconnect-and-refetch is the entire recovery story.
func (s *Server) publish(eventID, name string) {
	if s.broker == nil {
		return
	}
	s.broker.Publish(eventID, sse.Message{Name: name})
}
