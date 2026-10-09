package obs

import (
	"io"
	"log"
	"net/http"
	"sync/atomic"
	"time"
)

// Heartbeat tells whether the poll loop is still making progress. It is the
// basis of /healthz and deliberately knows nothing about the station API: a
// restart does not fix somebody else's outage.
type Heartbeat struct {
	deadline atomic.Int64 // unix nanoseconds until which the loop counts as alive
}

// Beat records a completed loop iteration. The loop counts as alive until
// maxAge has passed without another beat.
func (h *Heartbeat) Beat(now time.Time, maxAge time.Duration) {
	h.deadline.Store(now.Add(maxAge).UnixNano())
}

// Healthy reports whether the last beat is recent enough.
func (h *Heartbeat) Healthy(now time.Time) bool {
	return now.UnixNano() <= h.deadline.Load()
}

// NewServer returns the HTTP server for scraping and health checks.
func NewServer(addr string, m *Metrics, healthy func() bool, errorLog *log.Logger) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		m.WritePrometheus(w)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if !healthy() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, "poll loop stalled\n")
			return
		}
		_, _ = io.WriteString(w, "ok\n")
	})
	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          errorLog,
	}
}
