package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"basealert/internal/obs"
	"basealert/internal/wao"
)

func TestMain(m *testing.M) {
	// Tests of failing starts must not wait for the restart brake.
	minFailedLifetime = 0
	os.Exit(m.Run())
}

// --- fakes ---------------------------------------------------------------

type liveShow struct {
	DJ    string
	ID    int64
	Show  string
	Style string
	Start time.Time
	End   time.Time
}

// fakeAPI stands in for api.tb-group.fm.
type fakeAPI struct {
	srv    *httptest.Server
	mu     sync.Mutex
	status int
	body   string
	calls  int
}

func newFakeAPI(t *testing.T) *fakeAPI {
	f := &fakeAPI{status: 200}
	f.setLive(nil)
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls++
		w.WriteHeader(f.status)
		_, _ = w.Write([]byte(f.body))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// setLive makes the named stations live; all others play the playlist.
func (f *fakeAPI) setLive(shows map[string]liveShow) {
	out := map[string]any{}
	for _, name := range wao.StationNames() {
		s, ok := shows[name]
		if !ok {
			out[name] = map[string]any{"p": true, "l": 100}
			continue
		}
		out[name] = map[string]any{
			"dj": s.DJ, "id": s.ID, "sn": s.Show, "ss": s.Style,
			"st": s.Start.UnixMilli(), "en": s.End.UnixMilli(), "l": 100,
		}
	}
	body, _ := json.Marshal(out)
	f.mu.Lock()
	f.status, f.body = 200, string(body)
	f.mu.Unlock()
}

func (f *fakeAPI) fail(status int) {
	f.mu.Lock()
	f.status, f.body = status, `{"error":"down"}`
	f.mu.Unlock()
}

func (f *fakeAPI) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakePushover stands in for api.pushover.net. It answers with the queued
// statuses first and accepts everything after that.
type fakePushover struct {
	srv      *httptest.Server
	mu       sync.Mutex
	statuses []int
	messages []url.Values
}

func newFakePushover(t *testing.T) *fakePushover {
	f := &fakePushover{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		f.messages = append(f.messages, r.PostForm)
		status := 200
		if len(f.statuses) > 0 {
			status, f.statuses = f.statuses[0], f.statuses[1:]
		}
		w.WriteHeader(status)
		if status == 200 {
			_, _ = w.Write([]byte(`{"status":1,"request":"req-123"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":0,"errors":["application token is invalid"],"request":"req-err"}`))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakePushover) reply(statuses ...int) {
	f.mu.Lock()
	f.statuses = append(f.statuses, statuses...)
	f.mu.Unlock()
}

func (f *fakePushover) sent() []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]url.Values(nil), f.messages...)
}

func (f *fakePushover) titles() string {
	var titles []string
	for _, m := range f.sent() {
		titles = append(titles, m.Get("title"))
	}
	return strings.Join(titles, " | ")
}

// syncBuffer is a bytes.Buffer that may be written and read concurrently.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}

// --- harness -------------------------------------------------------------

type harness struct {
	t         *testing.T
	app       *app
	api       *fakeAPI
	push      *fakePushover
	logs      *syncBuffer
	now       time.Time
	cfgPath   string
	statePath string
}

var berlin = mustLocation("Europe/Berlin")

func mustLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// friday returns a time on Friday 2026-10-09 in Berlin.
func friday(hour, minute int) time.Time {
	return time.Date(2026, 10, 9, hour, minute, 0, 0, berlin)
}

var (
	blueCore  = liveShow{DJ: "BlueCore", ID: 525081, Show: "Happy Hardcore Bass Kick", Style: "Happy Hardcore", Start: friday(20, 0), End: friday(22, 0)}
	bazzShift = liveShow{DJ: "Bazz Shift", ID: 574956, Show: "Weekend Warm Up", Style: "Rawstyle", Start: friday(20, 0), End: friday(22, 0)}
)

const favoriteConfig = "stations: [TechnoBase, HardBase]\nfavorites: [BlueCore]\n"

func newHarness(t *testing.T, configYAML string) *harness {
	t.Helper()
	dir := t.TempDir()
	h := &harness{
		t:         t,
		api:       newFakeAPI(t),
		push:      newFakePushover(t),
		logs:      &syncBuffer{},
		now:       friday(20, 5),
		cfgPath:   filepath.Join(dir, "config.yaml"),
		statePath: filepath.Join(dir, "state.json"),
	}
	if configYAML != "" {
		h.writeConfig(configYAML)
	}
	h.app = h.newApp()
	return h
}

func (h *harness) settings() settings {
	return settings{
		Token: "app-token", User: "user-key",
		ConfigPath: h.cfgPath, StatePath: h.statePath,
		APIURL: h.api.srv.URL, PushoverURL: h.push.srv.URL,
		LogLevel: "debug", LogFormat: "json",
		Stdout: h.logs, Stderr: h.logs,
	}
}

// newApp builds an application instance the way a process start does.
func (h *harness) newApp() *app {
	h.t.Helper()
	logger, err := obs.NewLogger(h.logs, "json", "debug")
	if err != nil {
		h.t.Fatal(err)
	}
	a := newApp(h.settings(), logger, obs.NewMetrics("test"), func() time.Time { return h.now })
	a.restoreState()
	return a
}

func (h *harness) writeConfig(yaml string) {
	h.t.Helper()
	if err := os.WriteFile(h.cfgPath, []byte(yaml), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

// cycle runs one poll cycle and then lets a minute pass.
func (h *harness) cycle() {
	h.t.Helper()
	h.app.cycle(h.t.Context())
	h.now = h.now.Add(time.Minute)
}

// logLines returns the log lines written since the last call.
func (h *harness) logLines() []map[string]any {
	h.t.Helper()
	var lines []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(h.logs.String()), "\n") {
		if raw == "" {
			continue
		}
		var line map[string]any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			h.t.Fatalf("log line is not JSON: %q", raw)
		}
		for _, key := range []string{"time", "level", "msg", "event"} {
			if _, ok := line[key]; !ok {
				h.t.Errorf("log line lacks %q: %s", key, raw)
			}
		}
		lines = append(lines, line)
	}
	h.logs.Reset()
	return lines
}

// events returns the event names logged since the last call, without the
// per-poll debug lines.
func (h *harness) events() string {
	h.t.Helper()
	var names []string
	for _, line := range h.logLines() {
		if line["event"] != obs.EventPollCompleted {
			names = append(names, line["event"].(string))
		}
	}
	return strings.Join(names, " ")
}

func (h *harness) metrics() string {
	var buf bytes.Buffer
	h.app.met.WritePrometheus(&buf)
	return buf.String()
}

func (h *harness) wantMetric(series string) {
	h.t.Helper()
	if !strings.Contains(h.metrics(), series+"\n") {
		var related []string
		name, _, _ := strings.Cut(series, "{")
		name, _, _ = strings.Cut(name, " ")
		for _, line := range strings.Split(h.metrics(), "\n") {
			if strings.HasPrefix(line, name) {
				related = append(related, line)
			}
		}
		h.t.Errorf("metric missing: %s\nhave:\n%s", series, strings.Join(related, "\n"))
	}
}

func find(lines []map[string]any, event string) map[string]any {
	for _, line := range lines {
		if line["event"] == event {
			return line
		}
	}
	return nil
}

// --- the daemon's behaviour ------------------------------------------------

func TestFavoriteGoesLive(t *testing.T) {
	h := newHarness(t, favoriteConfig)
	h.api.setLive(map[string]liveShow{"TechnoBase": blueCore, "HardBase": bazzShift})
	h.cycle()

	sent := h.push.sent()
	if len(sent) != 1 {
		t.Fatalf("pushes = %d (%s), want 1", len(sent), h.push.titles())
	}
	want := map[string]string{
		"token":     "app-token",
		"user":      "user-key",
		"title":     "BlueCore ist live",
		"message":   "TechnoBase.FM – Happy Hardcore Bass Kick\nHappy Hardcore · 20:00–22:00 Uhr",
		"url":       "https://weareone.fm/#TechnoBase",
		"url_title": "Reinhören",
		"ttl":       "6900", // 20:05 until 22:00
	}
	for key, value := range want {
		if got := sent[0].Get(key); got != value {
			t.Errorf("push %s = %q, want %q", key, got, value)
		}
	}

	lines := h.logLines()
	var order []string
	for _, line := range lines {
		order = append(order, line["event"].(string))
	}
	if got := strings.Join(order, " "); got != "config.loaded station.live station.live notification.sent poll.completed" {
		t.Errorf("events = %s", got)
	}

	live := find(lines, obs.EventStationLive)
	wantLive := map[string]any{
		"level": "info", "station": "TechnoBase", "dj": "BlueCore", "dj_id": float64(525081),
		"show": "Happy Hardcore Bass Kick", "style": "Happy Hardcore",
		"show_start": "2026-10-09T18:00:00Z", "show_end": "2026-10-09T20:00:00Z",
		"favorite": true, "slot_active": false, "decision": "notify_favorite",
	}
	for key, value := range wantLive {
		if live[key] != value {
			t.Errorf("station.live %s = %v, want %v", key, live[key], value)
		}
	}
	if _, ok := live["reason"]; ok {
		t.Errorf("a due notification has no reason, got %v", live["reason"])
	}
	other := lines[2]
	if other["station"] != "HardBase" || other["decision"] != "none" || other["reason"] != "no_rule_matched" {
		t.Errorf("second station.live = %v", other)
	}
	note := find(lines, obs.EventNotificationSent)
	if note["kind"] != "favorite" || note["station"] != "TechnoBase" || note["pushover_request"] != "req-123" || note["title"] != "BlueCore ist live" {
		t.Errorf("notification.sent = %v", note)
	}
	loaded := find(lines, obs.EventConfigLoaded)
	if loaded["stations"] != "TechnoBase,HardBase" || loaded["favorites"] != float64(1) || loaded["reload"] != false {
		t.Errorf("config.loaded = %v", loaded)
	}

	h.wantMetric(`basealert_notifications_total{kind="favorite",result="sent",station="TechnoBase"} 1`)
	h.wantMetric(`basealert_notifications_total{kind="favorite",result="sent",station="HardBase"} 0`)
	h.wantMetric(`basealert_polls_total{result="ok"} 1`)
	h.wantMetric(`basealert_station_live{station="TechnoBase"} 1`)
	h.wantMetric(`basealert_station_favorite_live{station="TechnoBase"} 1`)
	h.wantMetric(`basealert_station_favorite_live{station="HardBase"} 0`)
	h.wantMetric(`basealert_station_slot_active{station="TechnoBase"} 0`)
	h.wantMetric(`basealert_config_last_reload_successful 1`)
	h.wantMetric(`basealert_config_favorites 1`)

	// The show goes on: nothing more to say.
	h.cycle()
	h.cycle()
	if len(h.push.sent()) != 1 {
		t.Errorf("pushes after two more polls = %s", h.push.titles())
	}
	if got := h.events(); got != "" {
		t.Errorf("events while nothing changes = %q", got)
	}
}

func TestSlotAnnouncesAnyDJ(t *testing.T) {
	h := newHarness(t, `
stations: [TechnoBase]
slots:
  - {days: [Fr], from: "20:00", to: "24:00"}
notify:
  slot: {priority: -1, sound: none}
`)
	h.api.setLive(map[string]liveShow{"TechnoBase": blueCore})
	h.cycle()

	sent := h.push.sent()
	if len(sent) != 1 {
		t.Fatalf("pushes = %s", h.push.titles())
	}
	want := map[string]string{
		"title":    "TechnoBase.FM ist live",
		"message":  "BlueCore – Happy Hardcore Bass Kick\nHappy Hardcore · bis 22:00 Uhr",
		"priority": "-1",
		"sound":    "none",
	}
	for key, value := range want {
		if got := sent[0].Get(key); got != value {
			t.Errorf("push %s = %q, want %q", key, got, value)
		}
	}
	if got := h.events(); got != "config.loaded slot.started station.live notification.sent" {
		t.Errorf("events = %s", got)
	}
	h.wantMetric(`basealert_station_slot_active{station="TechnoBase"} 1`)
	h.wantMetric(`basealert_notifications_total{kind="slot",result="sent",station="TechnoBase"} 1`)
	h.wantMetric(`basealert_config_slots 1`)
}

func TestTransientPushFailureIsRetried(t *testing.T) {
	h := newHarness(t, favoriteConfig)
	h.api.setLive(map[string]liveShow{"TechnoBase": blueCore})
	h.push.reply(500, 502)

	h.cycle()
	lines := h.logLines()
	failed := find(lines, obs.EventNotificationFailed)
	if failed == nil || failed["level"] != "warn" || failed["http_status"] != float64(500) || failed["kind"] != "favorite" {
		t.Errorf("notification.failed = %v", failed)
	}
	h.wantMetric(`basealert_notifications_total{kind="favorite",result="failed",station="TechnoBase"} 1`)

	h.cycle() // 502
	h.cycle() // accepted
	h.cycle() // nothing left to do
	if got := len(h.push.sent()); got != 3 {
		t.Errorf("requests to Pushover = %d, want 3", got)
	}
	h.wantMetric(`basealert_notifications_total{kind="favorite",result="failed",station="TechnoBase"} 2`)
	h.wantMetric(`basealert_notifications_total{kind="favorite",result="sent",station="TechnoBase"} 1`)
	if got := h.events(); got != "notification.failed notification.sent" {
		t.Errorf("events = %s", got)
	}
}

func TestRejectedPushIsNotRetried(t *testing.T) {
	h := newHarness(t, favoriteConfig)
	h.api.setLive(map[string]liveShow{"TechnoBase": blueCore})
	h.push.reply(400)

	h.cycle()
	rejected := find(h.logLines(), obs.EventNotificationRejected)
	if rejected == nil || rejected["level"] != "error" || rejected["http_status"] != float64(400) ||
		!strings.Contains(rejected["error"].(string), "application token is invalid") {
		t.Errorf("notification.rejected = %v", rejected)
	}
	h.cycle()
	h.cycle()
	if got := len(h.push.sent()); got != 1 {
		t.Errorf("requests to Pushover = %d, want exactly 1: rejected requests must not be repeated", got)
	}
	h.wantMetric(`basealert_notifications_total{kind="favorite",result="rejected",station="TechnoBase"} 1`)
}

func TestBrokenConfigKeepsTheOldOne(t *testing.T) {
	h := newHarness(t, favoriteConfig)
	h.cycle()
	h.logLines()

	h.writeConfig("favorites: [BlueCore\n")
	h.cycle()
	lines := h.logLines()
	invalid := find(lines, obs.EventConfigInvalid)
	if invalid == nil || invalid["level"] != "error" || invalid["active_config_kept"] != true || invalid["error"] == "" {
		t.Errorf("config.invalid = %v", invalid)
	}
	sent := h.push.sent()
	if len(sent) != 1 || sent[0].Get("title") != "BaseAlert: config.yaml fehlerhaft" ||
		!strings.Contains(sent[0].Get("message"), "Die bisherige Konfiguration bleibt aktiv.") {
		t.Fatalf("config error push = %v", sent)
	}
	h.wantMetric(`basealert_config_last_reload_successful 0`)
	h.wantMetric(`basealert_config_favorites 1`)
	h.wantMetric(`basealert_notifications_total{kind="config_error",result="sent"} 1`)

	// The old config is still in force, and the broken file is reported once.
	h.api.setLive(map[string]liveShow{"TechnoBase": blueCore})
	h.cycle()
	if got := h.push.titles(); got != "BaseAlert: config.yaml fehlerhaft | BlueCore ist live" {
		t.Errorf("pushes = %s", got)
	}

	h.writeConfig("stations: [TechnoBase, HardBase]\nfavorites: [BlueCore, Bazz Shift]\n")
	h.cycle()
	loaded := find(h.logLines(), obs.EventConfigLoaded)
	if loaded == nil || loaded["reload"] != true || loaded["favorites"] != float64(2) {
		t.Errorf("config.loaded after the fix = %v", loaded)
	}
	h.wantMetric(`basealert_config_last_reload_successful 1`)
	h.wantMetric(`basealert_config_favorites 2`)
}

func TestNoConfigAtStartup(t *testing.T) {
	h := newHarness(t, "")
	h.cycle()
	h.cycle()

	if h.api.callCount() != 0 {
		t.Errorf("API was polled %d times without a config", h.api.callCount())
	}
	sent := h.push.sent()
	if len(sent) != 1 || !strings.Contains(sent[0].Get("message"), "keine gültige Konfiguration") {
		t.Fatalf("pushes = %v", sent)
	}
	if got := h.events(); got != "config.invalid notification.sent" {
		t.Errorf("events = %s", got)
	}
	h.wantMetric(`basealert_config_last_reload_successful 0`)

	h.writeConfig(favoriteConfig)
	h.cycle()
	if h.api.callCount() != 1 {
		t.Errorf("API calls after the config appeared = %d", h.api.callCount())
	}
	if got := h.events(); got != "config.loaded" {
		t.Errorf("events = %s", got)
	}
}

func TestConfigErrorPushIsRetriedUntilDelivered(t *testing.T) {
	h := newHarness(t, "")
	h.push.reply(503)
	h.cycle()
	h.cycle()
	h.cycle()
	if got := len(h.push.sent()); got != 2 {
		t.Errorf("requests = %d, want 2 (one failed, one delivered)", got)
	}
	h.wantMetric(`basealert_notifications_total{kind="config_error",result="failed"} 1`)
	h.wantMetric(`basealert_notifications_total{kind="config_error",result="sent"} 1`)
}

func TestOutageAndRecovery(t *testing.T) {
	h := newHarness(t, favoriteConfig+"outage_alert_after: 5m\n")
	h.api.setLive(map[string]liveShow{"TechnoBase": blueCore})
	h.cycle() // 20:05, favourite announced
	h.logLines()

	h.api.fail(500)
	for range 5 { // 20:06 to 20:10: failing, but not long enough
		h.cycle()
	}
	lines := h.logLines()
	if len(lines) != 5 {
		t.Fatalf("want 5 poll.failed lines, got %d", len(lines))
	}
	last := lines[4]
	if last["event"] != "poll.failed" || last["level"] != "warn" || last["error_kind"] != "http_status" ||
		last["http_status"] != float64(500) || last["consecutive_failures"] != float64(5) {
		t.Errorf("poll.failed = %v", last)
	}
	if got := h.push.titles(); got != "BlueCore ist live" {
		t.Fatalf("pushes before the threshold = %s", got)
	}

	h.cycle() // 20:11, five minutes after the first failure
	lines = h.logLines()
	outage := find(lines, obs.EventUpstreamOutage)
	if outage == nil || outage["level"] != "error" || outage["since"] != "2026-10-09T18:06:00Z" {
		t.Errorf("upstream.outage = %v", outage)
	}
	sent := h.push.sent()
	if len(sent) != 2 || sent[1].Get("title") != "BaseAlert: Sender-API nicht erreichbar" ||
		!strings.Contains(sent[1].Get("message"), "Seit 20:06 Uhr") {
		t.Fatalf("outage push = %v", sent)
	}

	h.cycle() // still down: no second outage push
	h.cycle()
	if got := len(h.push.sent()); got != 2 {
		t.Errorf("pushes during the outage = %d, want 2", got)
	}
	h.logLines()

	h.api.setLive(map[string]liveShow{"TechnoBase": blueCore})
	h.cycle() // 20:14, back again
	lines = h.logLines()
	recovered := find(lines, obs.EventUpstreamRecovered)
	if recovered == nil || recovered["outage_seconds"] != float64(8*60) || recovered["failed_polls"] != float64(8) {
		t.Errorf("upstream.recovered = %v", recovered)
	}
	sent = h.push.sent()
	if len(sent) != 3 || sent[2].Get("title") != "BaseAlert: Sender-API wieder erreichbar" ||
		!strings.Contains(sent[2].Get("message"), "Ausfall von 20:06 bis 20:14 Uhr (8 min)") {
		t.Fatalf("recovery push = %v", sent)
	}
	// The favourite was announced before the outage and is not announced again.
	if find(lines, obs.EventStationLive) != nil {
		t.Error("the state must survive the outage")
	}

	h.wantMetric(`basealert_polls_total{result="http_status"} 8`)
	h.wantMetric(`basealert_polls_total{result="ok"} 2`)
	h.wantMetric(`basealert_notifications_total{kind="outage",result="sent"} 1`)
	h.wantMetric(`basealert_notifications_total{kind="recovery",result="sent"} 1`)
}

func TestOutagePushCanBeDisabled(t *testing.T) {
	h := newHarness(t, favoriteConfig+"outage_alert_after: 0\n")
	h.cycle()
	h.api.fail(503)
	for range 20 {
		h.cycle()
	}
	h.api.setLive(nil)
	h.cycle()

	if got := h.push.titles(); got != "" {
		t.Errorf("pushes = %s, want none", got)
	}
	// The log still tells the story.
	lines := h.logLines()
	if find(lines, obs.EventUpstreamOutage) == nil || find(lines, obs.EventUpstreamRecovered) == nil {
		t.Error("outage and recovery must be logged even when the push is disabled")
	}
}

func TestMalformedResponseCountsAsFailedPoll(t *testing.T) {
	h := newHarness(t, favoriteConfig)
	h.api.mu.Lock()
	h.api.body = `{"TechnoBase": {"moderator": "BlueCore"}, "HardBase": {"p": true}}`
	h.api.mu.Unlock()
	h.cycle()

	failed := find(h.logLines(), obs.EventPollFailed)
	if failed == nil || failed["error_kind"] != "invalid_response" {
		t.Errorf("poll.failed = %v", failed)
	}
	h.wantMetric(`basealert_polls_total{result="invalid_response"} 1`)
	if strings.Contains(h.metrics(), "basealert_station_live") {
		t.Error("no station gauges without a single good poll")
	}
}

func TestRestartKeepsMemory(t *testing.T) {
	h := newHarness(t, favoriteConfig)
	h.api.setLive(map[string]liveShow{"TechnoBase": blueCore})
	h.cycle()
	if _, err := os.Stat(h.statePath); err != nil {
		t.Fatalf("state file was not written: %v", err)
	}
	h.logLines()

	// A new process with the same state file.
	h.now = friday(20, 40)
	h.app = h.newApp()
	h.cycle()

	if got := h.push.titles(); got != "BlueCore ist live" {
		t.Errorf("pushes after the restart = %s, want no second one", got)
	}
	if got := h.events(); got != "state.restored config.loaded" {
		t.Errorf("events after the restart = %s", got)
	}
}

func TestUnwritableStateIsReportedOnce(t *testing.T) {
	h := newHarness(t, favoriteConfig)
	h.statePath = filepath.Join(t.TempDir(), "missing", "state.json")
	h.app = h.newApp()
	h.api.setLive(map[string]liveShow{"TechnoBase": blueCore})
	h.cycle()
	h.cycle()
	h.cycle()

	var failures int
	for _, line := range h.logLines() {
		if line["event"] == obs.EventStateSaveFailed {
			failures++
			if line["level"] != "warn" {
				t.Errorf("state.save_failed level = %v", line["level"])
			}
		}
	}
	if failures != 1 {
		t.Errorf("state.save_failed logged %d times, want once", failures)
	}
	if len(h.push.sent()) != 1 {
		t.Error("notifications must work without a state file")
	}
}

func TestHeartbeatFollowsTheLoop(t *testing.T) {
	h := newHarness(t, favoriteConfig)
	if h.app.beat.Healthy(time.Now()) {
		t.Error("no cycle yet")
	}
	h.cycle()
	if !h.app.beat.Healthy(time.Now()) {
		t.Error("healthy right after a cycle")
	}
	// Healthy for three poll intervals, even while the API is down.
	h.api.fail(500)
	h.cycle()
	if !h.app.beat.Healthy(time.Now().Add(179 * time.Second)) {
		t.Error("an API outage must not make the process unhealthy")
	}
	if h.app.beat.Healthy(time.Now().Add(181 * time.Second)) {
		t.Error("a stalled loop must turn unhealthy after three intervals")
	}
}
