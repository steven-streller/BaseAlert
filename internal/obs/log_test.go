package obs

import (
	"bytes"
	"encoding/json"
	"log"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

func decodeLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if raw == "" {
			continue
		}
		var line map[string]any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("not JSON: %q (%v)", raw, err)
		}
		lines = append(lines, line)
	}
	return lines
}

func TestJSONLogShape(t *testing.T) {
	var buf bytes.Buffer
	logger, err := NewLogger(&buf, "json", "info")
	if err != nil {
		t.Fatal(err)
	}
	berlin, _ := time.LoadLocation("Europe/Berlin")
	showStart := time.Date(2026, 10, 9, 20, 0, 0, 0, berlin)

	Log(logger, slog.LevelInfo, EventStationLive, "DJ session started",
		slog.String("station", "TechnoBase"),
		slog.Int64("dj_id", 525081),
		slog.Bool("favorite", true),
		slog.Time("show_start", showStart),
	)
	Log(logger, slog.LevelWarn, EventPollFailed, "poll failed", slog.String("error", "line one\nline two"))
	Log(logger, slog.LevelError, EventUpstreamOutage, "station API unreachable")
	Log(logger, slog.LevelDebug, EventPollCompleted, "poll completed")

	if n := strings.Count(buf.String(), "\n"); n != 3 {
		t.Fatalf("want 3 physical lines (debug is filtered, newlines are escaped), got %d:\n%s", n, buf.String())
	}
	lines := decodeLines(t, &buf)

	first := lines[0]
	if first["level"] != "info" || first["msg"] != "DJ session started" || first["event"] != "station.live" {
		t.Errorf("standard fields = %v", first)
	}
	if first["station"] != "TechnoBase" || first["dj_id"] != float64(525081) || first["favorite"] != true {
		t.Errorf("attributes = %v", first)
	}
	if first["show_start"] != "2026-10-09T18:00:00Z" {
		t.Errorf("show_start = %v, want UTC", first["show_start"])
	}
	stamp, ok := first["time"].(string)
	if !ok || !strings.HasSuffix(stamp, "Z") {
		t.Errorf("time = %v, want UTC with Z suffix", first["time"])
	}
	if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil {
		t.Errorf("time %q is not RFC 3339: %v", stamp, err)
	}
	if lines[1]["level"] != "warn" || lines[2]["level"] != "error" {
		t.Errorf("levels = %v, %v", lines[1]["level"], lines[2]["level"])
	}
	for i, line := range lines {
		for _, key := range []string{"time", "level", "msg", "event"} {
			if _, ok := line[key]; !ok {
				t.Errorf("line %d lacks %q: %v", i, key, line)
			}
		}
	}
}

func TestLogLevels(t *testing.T) {
	for level, wantLines := range map[string]int{"debug": 4, "info": 3, "": 3, "WARN": 2, "warning": 2, "error": 1} {
		var buf bytes.Buffer
		logger, err := NewLogger(&buf, "json", level)
		if err != nil {
			t.Fatalf("level %q: %v", level, err)
		}
		for _, l := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError} {
			Log(logger, l, EventPollCompleted, "x")
		}
		if got := len(decodeLines(t, &buf)); got != wantLines {
			t.Errorf("level %q: %d lines, want %d", level, got, wantLines)
		}
	}
	if _, err := NewLogger(&bytes.Buffer{}, "json", "loud"); err == nil {
		t.Error("unknown level must be rejected")
	}
	if _, err := NewLogger(&bytes.Buffer{}, "xml", "info"); err == nil {
		t.Error("unknown format must be rejected")
	}
}

func TestTextFormat(t *testing.T) {
	var buf bytes.Buffer
	logger, err := NewLogger(&buf, "text", "info")
	if err != nil {
		t.Fatal(err)
	}
	Log(logger, slog.LevelInfo, EventAppStarted, "started", slog.String("version", "dev"))
	out := buf.String()
	for _, want := range []string{"level=info", "msg=started", "event=app.started", "version=dev"} {
		if !strings.Contains(out, want) {
			t.Errorf("text line %q lacks %q", out, want)
		}
	}
}

func TestLibraryOutputBecomesStructured(t *testing.T) {
	var buf bytes.Buffer
	logger, _ := NewLogger(&buf, "json", "info")
	CaptureStdLog(logger)
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
		log.SetFlags(log.LstdFlags)
	})

	log.Printf("ERROR: metrics: cannot open %s: %s", "/proc/self/io", "permission denied")
	StdLogger(logger, EventHTTPError).Print("http: panic serving 10.0.0.1:1234: boom\ngoroutine 7 [running]:\nmain.x()")

	lines := decodeLines(t, &buf)
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %s", len(lines), buf.String())
	}
	if lines[0]["event"] != "library.log" || lines[0]["level"] != "warn" ||
		lines[0]["msg"] != "ERROR: metrics: cannot open /proc/self/io: permission denied" {
		t.Errorf("library line = %v", lines[0])
	}
	if lines[1]["event"] != "http.error" || !strings.Contains(lines[1]["msg"].(string), "goroutine 7") {
		t.Errorf("http error line = %v", lines[1])
	}
}

func TestEventNamesAreUniqueAndWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, event := range Events {
		if seen[event] {
			t.Errorf("duplicate event %q", event)
		}
		seen[event] = true
		domain, action, ok := strings.Cut(event, ".")
		if !ok || domain == "" || action == "" || strings.ToLower(event) != event || strings.ContainsAny(event, " -") {
			t.Errorf("event %q is not of the form domain.action", event)
		}
	}
}
