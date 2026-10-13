package server

import (
	"fmt"
	"net/http"
	"time"
)

// handleLive streams a project's new logs, errors, events, sessions and
// detections to the dashboard as Server-Sent Events.
func (s *Server) handleLive(w http.ResponseWriter, r *http.Request, p *project) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		// requestLogger wraps the writer; unwrap through ResponseController.
		rc := http.NewResponseController(w)
		flusher = flushFunc(func() { rc.Flush() })
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no") // disable proxy buffering (nginx)
	w.WriteHeader(http.StatusOK)

	ch, cancel := s.broker.Subscribe(p.ID)
	defer cancel()
	// Long-lived: lift the server's write deadline for this response.
	http.NewResponseController(w).SetWriteDeadline(time.Time{})

	fmt.Fprint(w, "retry: 3000\n\n")
	flusher.Flush()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return nil
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case msg := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		}
	}
}

type flushFunc func()

func (f flushFunc) Flush() { f() }
