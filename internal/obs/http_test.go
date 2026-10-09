package obs

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestServerEndpoints(t *testing.T) {
	healthy := true
	srv := httptest.NewServer(NewServer("", populated(), func() bool { return healthy }, nil).Handler)
	defer srv.Close()

	get := func(method, path string) (int, string, string) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("Content-Type"), string(body)
	}

	status, contentType, body := get(http.MethodGet, "/metrics")
	if status != 200 || contentType != "text/plain; version=0.0.4; charset=utf-8" {
		t.Errorf("/metrics: status %d, content type %q", status, contentType)
	}
	for _, want := range []string{"# TYPE basealert_polls_total counter", `basealert_station_live{station="TechnoBase"} 1`, "go_goroutines "} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics lacks %q", want)
		}
	}

	if status, _, body := get(http.MethodGet, "/healthz"); status != 200 || body != "ok\n" {
		t.Errorf("/healthz healthy: %d %q", status, body)
	}
	healthy = false
	if status, _, body := get(http.MethodGet, "/healthz"); status != 503 || !strings.Contains(body, "stalled") {
		t.Errorf("/healthz unhealthy: %d %q", status, body)
	}

	if status, _, _ := get(http.MethodPost, "/metrics"); status != http.StatusMethodNotAllowed {
		t.Errorf("POST /metrics: %d, want 405", status)
	}
	for _, path := range []string{"/", "/metrics/", "/debug/pprof/"} {
		if status, _, _ := get(http.MethodGet, path); status != http.StatusNotFound {
			t.Errorf("GET %s: %d, want 404", path, status)
		}
	}
}

func TestServerHasTimeouts(t *testing.T) {
	srv := NewServer(":8080", NewMetrics("dev"), func() bool { return true }, nil)
	if srv.ReadHeaderTimeout <= 0 || srv.ReadTimeout <= 0 || srv.WriteTimeout <= 0 || srv.IdleTimeout <= 0 {
		t.Errorf("timeouts not set: %+v", srv)
	}
	if srv.Addr != ":8080" {
		t.Errorf("addr = %q", srv.Addr)
	}
}

func TestHeartbeat(t *testing.T) {
	var hb Heartbeat
	start := time.Unix(1_791_568_800, 0)
	if hb.Healthy(start) {
		t.Error("no beat yet: must not be healthy")
	}
	hb.Beat(start, 3*time.Minute)
	for offset, want := range map[time.Duration]bool{
		0:                           true,
		time.Minute:                 true,
		3 * time.Minute:             true,
		3*time.Minute + time.Second: false,
		time.Hour:                   false,
	} {
		if got := hb.Healthy(start.Add(offset)); got != want {
			t.Errorf("after %s: healthy = %v, want %v", offset, got, want)
		}
	}
	// A later beat extends the deadline again.
	hb.Beat(start.Add(time.Hour), 3*time.Minute)
	if !hb.Healthy(start.Add(time.Hour + time.Minute)) {
		t.Error("a new beat must make the loop healthy again")
	}
}
