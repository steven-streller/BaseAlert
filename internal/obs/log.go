// Package obs holds what makes BaseAlert observable: the log format, the
// metrics and the HTTP endpoints for scraping and health checks. Event names
// and metric names are defined here and nowhere else, because dashboards and
// queries depend on them.
package obs

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"strings"
	"time"
)

// Event names, the "event" field of every log line.
const (
	EventAppStarted           = "app.started"
	EventAppStopping          = "app.stopping"
	EventAppExitDelayed       = "app.exit_delayed"
	EventAppPanic             = "app.panic"
	EventConfigLoaded         = "config.loaded"
	EventConfigInvalid        = "config.invalid"
	EventStateRestored        = "state.restored"
	EventStateLoadFailed      = "state.load_failed"
	EventStateSaveFailed      = "state.save_failed"
	EventPollCompleted        = "poll.completed"
	EventPollFailed           = "poll.failed"
	EventUpstreamOutage       = "upstream.outage"
	EventUpstreamRecovered    = "upstream.recovered"
	EventStationLive          = "station.live"
	EventStationPlaylist      = "station.playlist"
	EventSlotStarted          = "slot.started"
	EventSlotEnded            = "slot.ended"
	EventNotificationSent     = "notification.sent"
	EventNotificationFailed   = "notification.failed"
	EventNotificationRejected = "notification.rejected"
	EventHTTPListening        = "http.listening"
	EventHTTPError            = "http.error"
	EventLibraryLog           = "library.log"
)

// Events lists every event name the application can log.
var Events = []string{
	EventAppStarted, EventAppStopping, EventAppExitDelayed, EventAppPanic,
	EventConfigLoaded, EventConfigInvalid,
	EventStateRestored, EventStateLoadFailed, EventStateSaveFailed,
	EventPollCompleted, EventPollFailed,
	EventUpstreamOutage, EventUpstreamRecovered,
	EventStationLive, EventStationPlaylist,
	EventSlotStarted, EventSlotEnded,
	EventNotificationSent, EventNotificationFailed, EventNotificationRejected,
	EventHTTPListening, EventHTTPError, EventLibraryLog,
}

// NewLogger returns a logger that writes one line per event to w.
//
// The "json" format is meant for Loki: flat snake_case fields, "time" in UTC,
// "level" in lower case. The "text" format is for reading logs in a terminal.
func NewLogger(w io.Writer, format, level string) (*slog.Logger, error) {
	var lvl slog.Level
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		lvl = slog.LevelDebug
	case "", "info":
		lvl = slog.LevelInfo
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		return nil, fmt.Errorf("unknown log level %q (debug, info, warn, error)", level)
	}
	opts := &slog.HandlerOptions{Level: lvl, ReplaceAttr: normalize}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "json":
		return slog.New(slog.NewJSONHandler(w, opts)), nil
	case "text":
		return slog.New(slog.NewTextHandler(w, opts)), nil
	default:
		return nil, fmt.Errorf("unknown log format %q (json, text)", format)
	}
}

func normalize(groups []string, a slog.Attr) slog.Attr {
	if len(groups) > 0 {
		return a
	}
	switch v := a.Value.Any().(type) {
	case time.Time:
		// Every timestamp is logged in UTC, the built-in one included.
		a.Value = slog.TimeValue(v.UTC())
	case slog.Level:
		if a.Key == slog.LevelKey {
			a.Value = slog.StringValue(strings.ToLower(v.String()))
		}
	}
	return a
}

// Log writes one event. All application logging goes through this function,
// which guarantees that every line carries an "event".
func Log(l *slog.Logger, level slog.Level, event, msg string, attrs ...slog.Attr) {
	all := make([]slog.Attr, 0, len(attrs)+1)
	all = append(all, slog.String("event", event))
	all = append(all, attrs...)
	l.LogAttrs(context.Background(), level, msg, all...)
}

// lineWriter turns each write into one log event.
type lineWriter struct {
	logger *slog.Logger
	level  slog.Level
	event  string
}

func (w lineWriter) Write(p []byte) (int, error) {
	Log(w.logger, w.level, w.event, strings.TrimSpace(string(p)))
	return len(p), nil
}

// CaptureStdLog routes the standard log package through l. Libraries write
// their diagnostics there, and without this they would show up as lines that
// are not JSON.
func CaptureStdLog(l *slog.Logger) {
	log.SetFlags(0)
	log.SetPrefix("")
	log.SetOutput(lineWriter{logger: l, level: slog.LevelWarn, event: EventLibraryLog})
}

// StdLogger returns a standard logger that reports through l under event. It
// is meant for http.Server.ErrorLog.
func StdLogger(l *slog.Logger, event string) *log.Logger {
	return log.New(lineWriter{logger: l, level: slog.LevelWarn, event: event}, "", 0)
}
