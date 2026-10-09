package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"basealert/internal/config"
	"basealert/internal/engine"
	"basealert/internal/obs"
)

// --- push texts ------------------------------------------------------------

func parseConfig(t *testing.T, yaml string) *config.Config {
	t.Helper()
	cfg, err := config.Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestStationMessage(t *testing.T) {
	cfg := parseConfig(t, "notify:\n  favorite: {priority: 1, sound: siren}\n  slot: {priority: -1}\n")
	full := engine.Notification{
		Kind: engine.KindFavorite, Station: "TechnoBase", DJ: "BlueCore", DJID: 525081,
		Show: "Happy Hardcore Bass Kick", Style: "Happy Hardcore", Start: friday(20, 0), End: friday(22, 0),
	}

	t.Run("favourite", func(t *testing.T) {
		m := stationMessage(full, cfg, friday(20, 0))
		if m.Title != "BlueCore ist live" {
			t.Errorf("title = %q", m.Title)
		}
		if m.Body != "TechnoBase.FM – Happy Hardcore Bass Kick\nHappy Hardcore · 20:00–22:00 Uhr" {
			t.Errorf("body = %q", m.Body)
		}
		if m.URL != "https://weareone.fm/#TechnoBase" || m.URLTitle != "Reinhören" {
			t.Errorf("link = %q (%q)", m.URL, m.URLTitle)
		}
		if m.Priority != 1 || m.Sound != "siren" {
			t.Errorf("priority/sound = %d/%q", m.Priority, m.Sound)
		}
		if m.TTL != 2*time.Hour {
			t.Errorf("ttl = %s, want the remaining show time", m.TTL)
		}
	})

	t.Run("slot", func(t *testing.T) {
		n := full
		n.Kind = engine.KindSlot
		m := stationMessage(n, cfg, friday(21, 30))
		if m.Title != "TechnoBase.FM ist live" {
			t.Errorf("title = %q", m.Title)
		}
		if m.Body != "BlueCore – Happy Hardcore Bass Kick\nHappy Hardcore · bis 22:00 Uhr" {
			t.Errorf("body = %q", m.Body)
		}
		if m.Priority != -1 || m.Sound != "" {
			t.Errorf("priority/sound = %d/%q", m.Priority, m.Sound)
		}
		if m.TTL != 30*time.Minute {
			t.Errorf("ttl = %s", m.TTL)
		}
	})

	t.Run("sparse data", func(t *testing.T) {
		n := engine.Notification{Kind: engine.KindFavorite, Station: "HardBase", DJ: "Bazz Shift"}
		m := stationMessage(n, cfg, friday(20, 0))
		if m.Title != "Bazz Shift ist live" || m.Body != "HardBase.FM" {
			t.Errorf("message = %q / %q", m.Title, m.Body)
		}
		if m.TTL != 0 {
			t.Errorf("ttl = %s, want none without an end time", m.TTL)
		}

		n = engine.Notification{Kind: engine.KindSlot, Station: "HardBase", DJ: "Bazz Shift", Style: "Rawstyle", End: friday(22, 0)}
		if m := stationMessage(n, cfg, friday(20, 0)); m.Body != "Bazz Shift\nRawstyle · bis 22:00 Uhr" {
			t.Errorf("body = %q", m.Body)
		}
	})

	t.Run("ttl limits", func(t *testing.T) {
		if m := stationMessage(full, cfg, friday(21, 59).Add(50*time.Second)); m.TTL != time.Minute {
			t.Errorf("ttl just before the end = %s, want the one minute minimum", m.TTL)
		}
		if m := stationMessage(full, cfg, friday(22, 30)); m.TTL != 0 {
			t.Errorf("ttl for an overrunning show = %s, want none", m.TTL)
		}
	})

	t.Run("times follow the configured timezone", func(t *testing.T) {
		utc := parseConfig(t, "timezone: UTC\n")
		if m := stationMessage(full, utc, friday(20, 0)); !strings.Contains(m.Body, "18:00–20:00 Uhr") {
			t.Errorf("body = %q", m.Body)
		}
	})
}

func TestSystemMessages(t *testing.T) {
	err := fmt.Errorf("favorites: Eintrag 2 ist leer")
	if m := configErrorMessage(err, true); m.Title != "BaseAlert: config.yaml fehlerhaft" ||
		m.Body != "favorites: Eintrag 2 ist leer\nDie bisherige Konfiguration bleibt aktiv." {
		t.Errorf("config error (old config kept) = %q / %q", m.Title, m.Body)
	}
	if m := configErrorMessage(err, false); !strings.Contains(m.Body, "keine gültige Konfiguration aktiv") {
		t.Errorf("config error (no config) = %q", m.Body)
	}

	outage := outageMessage(friday(20, 6), fmt.Errorf("unexpected status 500 Internal Server Error"), berlin)
	if outage.Body != "Seit 20:06 Uhr schlägt der Abruf fehl (unexpected status 500 Internal Server Error). Sendestarts werden solange nicht erkannt." {
		t.Errorf("outage = %q", outage.Body)
	}
	recovery := recoveryMessage(friday(20, 6), friday(22, 11), berlin)
	if recovery.Body != "Ausfall von 20:06 bis 22:11 Uhr (2 h 05 min). Sendestarts in dieser Zeit wurden möglicherweise nicht gemeldet." {
		t.Errorf("recovery = %q", recovery.Body)
	}
}

func TestGermanDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		8 * time.Minute:                 "8 min",
		59*time.Minute + 20*time.Second: "59 min",
		59*time.Minute + 40*time.Second: "1 h 00 min",
		125 * time.Minute:               "2 h 05 min",
		26 * time.Hour:                  "26 h 00 min",
	} {
		if got := germanDuration(d); got != want {
			t.Errorf("germanDuration(%s) = %q, want %q", d, got, want)
		}
	}
}

// --- commands --------------------------------------------------------------

// command runs a subcommand and returns its exit code and output.
func command(set settings, args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	set.Stdout, set.Stderr = &out, &errOut
	code = run(args, set)
	return code, out.String(), errOut.String()
}

func TestVersionHelpAndUnknownCommand(t *testing.T) {
	if code, out, _ := command(settings{}, "version"); code != 0 || out != version+"\n" {
		t.Errorf("version: %d %q", code, out)
	}
	if code, out, _ := command(settings{}, "help"); code != 0 || !strings.Contains(out, "Aufruf: basealert") {
		t.Errorf("help: %d %q", code, out)
	}
	code, out, errOut := command(settings{}, "frobnicate")
	if code != 2 || out != "" || !strings.Contains(errOut, `unbekanntes Kommando "frobnicate"`) {
		t.Errorf("unknown: %d %q %q", code, out, errOut)
	}
}

func TestSettingsFromEnvironment(t *testing.T) {
	env := map[string]string{
		"PUSHOVER_TOKEN":      " tok ",
		"BASEALERT_STATE":     "",
		"BASEALERT_HTTP_ADDR": "127.0.0.1:9000",
	}
	set := loadSettings(func(key string) (string, bool) { v, ok := env[key]; return v, ok }, io.Discard, io.Discard)
	if set.Token != "tok" || set.User != "" {
		t.Errorf("credentials = %q / %q", set.Token, set.User)
	}
	if set.ConfigPath != "/config/config.yaml" || set.LogLevel != "info" || set.LogFormat != "json" {
		t.Errorf("defaults = %+v", set)
	}
	if set.StatePath != "" {
		t.Errorf("an explicitly empty BASEALERT_STATE must disable persistence, got %q", set.StatePath)
	}
	if set.HTTPAddr != "127.0.0.1:9000" {
		t.Errorf("http addr = %q", set.HTTPAddr)
	}
	if unset := loadSettings(func(string) (string, bool) { return "", false }, io.Discard, io.Discard); unset.StatePath != "/data/state.json" || unset.HTTPAddr != ":8080" {
		t.Errorf("defaults when unset = %+v", unset)
	}
}

func TestNowCommand(t *testing.T) {
	h := newHarness(t, "stations: [TechnoBase, HardBase, TranceBase]\nfavorites: [525081]\nslots:\n  - {from: '00:00', to: '24:00', stations: [HardBase, TranceBase]}\n")
	h.api.setLive(map[string]liveShow{"TechnoBase": blueCore, "HardBase": bazzShift})

	code, out, errOut := command(h.settings(), "now")
	if code != 0 || errOut != "" {
		t.Fatalf("now: exit %d, stderr %q", code, errOut)
	}
	if !strings.Contains(out, "(Europe/Berlin), 1 Favoriten, 1 Zeitslots") {
		t.Errorf("header missing:\n%s", out)
	}
	rows := map[string][]string{}
	for _, line := range strings.Split(out, "\n") {
		if fields := strings.Split(line, "  "); len(fields) > 1 {
			var cells []string
			for _, f := range fields {
				if f = strings.TrimSpace(f); f != "" {
					cells = append(cells, f)
				}
			}
			rows[cells[0]] = cells
		}
	}
	want := map[string]string{
		"TechnoBase": "TechnoBase|live|BlueCore|525081|Happy Hardcore Bass Kick|20:00–22:00|ja|–",
		"HardBase":   "HardBase|live|Bazz Shift|574956|Weekend Warm Up|20:00–22:00|–|aktiv",
		"TranceBase": "TranceBase|Playlist|–|–|–|–|–|aktiv",
	}
	for station, row := range want {
		if got := strings.Join(rows[station], "|"); got != row {
			t.Errorf("%s row = %q\nwant        %q", station, got, row)
		}
	}
	if _, ok := rows["HouseTime"]; ok {
		t.Error("HouseTime is not watched and must not be listed")
	}
}

func TestNowCommandProblems(t *testing.T) {
	h := newHarness(t, favoriteConfig)

	t.Run("missing config falls back to defaults", func(t *testing.T) {
		set := h.settings()
		set.ConfigPath = filepath.Join(t.TempDir(), "none.yaml")
		code, out, errOut := command(set, "now")
		if code != 0 || !strings.Contains(errOut, "nicht gefunden") || !strings.Contains(out, "TranceBase") {
			t.Errorf("exit %d, stderr %q, stdout %q", code, errOut, out)
		}
	})

	t.Run("broken config is an error", func(t *testing.T) {
		h.writeConfig("favourites: [x]\n")
		code, out, errOut := command(h.settings(), "now")
		if code != 1 || out != "" || !strings.Contains(errOut, "favourites") {
			t.Errorf("exit %d, stderr %q, stdout %q", code, errOut, out)
		}
		h.writeConfig(favoriteConfig)
	})

	t.Run("API failure is an error", func(t *testing.T) {
		h.api.fail(500)
		code, _, errOut := command(h.settings(), "now")
		if code != 1 || !strings.Contains(errOut, "Sender-API") {
			t.Errorf("exit %d, stderr %q", code, errOut)
		}
	})
}

func TestDJsCommand(t *testing.T) {
	// The schedule lists the same DJ on two days and a second DJ once.
	inTwoDays := time.Now().Add(48 * time.Hour).Truncate(time.Hour)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var shows []map[string]any
		switch r.URL.Path {
		case "/v1/showplan/5/0":
			shows = append(shows, map[string]any{"n": "Old Show", "m": "Orca", "mi": 1085, "s": time.Now().Add(-26 * time.Hour).UnixMilli(), "e": time.Now().Add(-24 * time.Hour).UnixMilli()})
		case "/v1/showplan/5/3":
			shows = append(shows,
				map[string]any{"n": "Explosive Sounds", "m": "Orca", "mi": 1085, "s": inTwoDays.UnixMilli(), "e": inTwoDays.Add(2 * time.Hour).UnixMilli()},
				map[string]any{"n": "Binary Sounds", "m": "BitPitchy", "mi": 476427, "s": inTwoDays.Add(2 * time.Hour).UnixMilli(), "e": inTwoDays.Add(4 * time.Hour).UnixMilli()})
		}
		if shows == nil {
			shows = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(shows)
	}))
	defer srv.Close()

	showplanPause = 0
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("stations: [TechnoBase]\nfavorites: [1085]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := command(settings{ConfigPath: cfgPath, APIURL: srv.URL}, "djs")
	if code != 0 || errOut != "" {
		t.Fatalf("djs: exit %d, stderr %q", code, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 5 || !strings.HasPrefix(lines[0], "DJ ") {
		t.Fatalf("output:\n%s", out)
	}
	nextShow := func(start time.Time) string {
		local := start.In(berlin)
		return fmt.Sprintf("%s %s TechnoBase", weekdays[local.Weekday()], local.Format("02.01. 15:04"))
	}
	// Sorted by name; the show from yesterday does not count as "next".
	want := []string{
		"BitPitchy|476427|TechnoBase|" + nextShow(inTwoDays.Add(2*time.Hour)) + "|–",
		"Orca|1085|TechnoBase|" + nextShow(inTwoDays) + "|ja",
	}
	for i, row := range want {
		var cells []string
		for _, f := range strings.Split(lines[i+1], "  ") {
			if f = strings.TrimSpace(f); f != "" {
				cells = append(cells, f)
			}
		}
		if got := strings.Join(cells, "|"); got != row {
			t.Errorf("row %d = %q\nwant    %q", i+1, got, row)
		}
	}
	if lines[4] != "2 DJs im Sendeplan von gestern bis in sieben Tagen." {
		t.Errorf("summary = %q", lines[4])
	}
}

func TestTestCommand(t *testing.T) {
	push := newFakePushover(t)
	set := settings{Token: "app-token", User: "user-key", Device: "phone", PushoverURL: push.srv.URL}

	code, out, errOut := command(set, "test")
	if code != 0 || errOut != "" || !strings.Contains(out, "req-123") {
		t.Fatalf("test: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	sent := push.sent()
	if len(sent) != 1 || sent[0].Get("title") != "BaseAlert Test" || sent[0].Get("device") != "phone" {
		t.Errorf("test push = %v", sent)
	}

	push.reply(400)
	if code, _, errOut := command(set, "test"); code != 1 || !strings.Contains(errOut, "application token is invalid") {
		t.Errorf("rejected test push: exit %d, stderr %q", code, errOut)
	}
	if code, _, errOut := command(settings{}, "test"); code != 1 || !strings.Contains(errOut, "PUSHOVER_TOKEN") {
		t.Errorf("missing credentials: exit %d, stderr %q", code, errOut)
	}
}

func TestHealthCommand(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Errorf("health asked for %s", r.URL.Path)
		}
		w.WriteHeader(status)
	}))
	addr := srv.Listener.Addr().String()
	_, port, _ := strings.Cut(addr, ":")

	if code, out, _ := command(settings{HTTPAddr: addr}, "health"); code != 0 || out != "ok\n" {
		t.Errorf("healthy: exit %d, stdout %q", code, out)
	}
	// The daemon's listen address ":8080" has no host; the check goes to localhost.
	if code, _, errOut := command(settings{HTTPAddr: ":" + port}, "health"); code != 0 {
		t.Errorf("address without host: exit %d, stderr %q", code, errOut)
	}
	status = http.StatusServiceUnavailable
	if code, _, errOut := command(settings{HTTPAddr: addr}, "health"); code != 1 || !strings.Contains(errOut, "503") {
		t.Errorf("unhealthy: exit %d, stderr %q", code, errOut)
	}
	srv.Close()
	if code, _, _ := command(settings{HTTPAddr: addr}, "health"); code != 1 {
		t.Errorf("daemon gone: exit %d, want 1", code)
	}
	if code, _, errOut := command(settings{HTTPAddr: ""}, "health"); code != 1 || !strings.Contains(errOut, "abgeschaltet") {
		t.Errorf("server disabled: exit %d, stderr %q", code, errOut)
	}
}

// --- the daemon as a whole ---------------------------------------------------

func TestDaemonNeedsCredentials(t *testing.T) {
	h := newHarness(t, favoriteConfig)
	set := h.settings()
	set.Token = ""
	if code := run(nil, set); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	lines := h.logLines()
	if len(lines) != 1 || lines[0]["level"] != "error" || lines[0]["reason"] != "missing_credentials" {
		t.Errorf("log = %v", lines)
	}
}

// A daemon that cannot start must not exit at once: podman restarts without
// a pause, which would turn a missing token into a tight restart loop.
func TestFailingDaemonExitsSlowly(t *testing.T) {
	minFailedLifetime = 400 * time.Millisecond
	t.Cleanup(func() { minFailedLifetime = 0 })
	h := newHarness(t, favoriteConfig)
	set := h.settings()
	set.User = ""

	started := time.Now()
	if code := run(nil, set); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if lived := time.Since(started); lived < 400*time.Millisecond || lived > 5*time.Second {
		t.Errorf("process lived %s, want at least the 400ms brake", lived)
	}
	lines := h.logLines()
	if len(lines) != 2 || lines[0]["reason"] != "missing_credentials" ||
		lines[1]["event"] != obs.EventAppExitDelayed || lines[1]["level"] != "info" {
		t.Errorf("log = %v", lines)
	}

	// Unusable log settings are held back as well, without a logger to say so.
	set.LogFormat = "xml"
	started = time.Now()
	if code := run(nil, set); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if lived := time.Since(started); lived < 400*time.Millisecond {
		t.Errorf("process lived %s, want at least the 400ms brake", lived)
	}
}

func TestExitDelayYieldsToSIGTERM(t *testing.T) {
	minFailedLifetime = 30 * time.Second
	t.Cleanup(func() { minFailedLifetime = 0 })
	h := newHarness(t, favoriteConfig)
	set := h.settings()
	set.Token = ""

	exit := make(chan int, 1)
	started := time.Now()
	go func() { exit <- run(nil, set) }()
	waitFor(t, "the exit delay to begin", func() bool { return strings.Contains(h.logs.String(), obs.EventAppExitDelayed) })
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-exit:
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if lived := time.Since(started); lived > 5*time.Second {
			t.Errorf("stopping took %s, the brake must yield to SIGTERM", lived)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("daemon ignored SIGTERM during the exit delay")
	}
}

func TestExitIsNotDelayedInATerminalOrAfterALongRun(t *testing.T) {
	minFailedLifetime = 30 * time.Second
	t.Cleanup(func() { minFailedLifetime = 0 })
	logger, _ := obs.NewLogger(io.Discard, "json", "info")

	// A process that has lived long enough exits at once.
	started := time.Now()
	holdBeforeExit(settings{Stdout: io.Discard}, logger, time.Now().Add(-time.Minute))
	if waited := time.Since(started); waited > time.Second {
		t.Errorf("waited %s after a long run", waited)
	}

	if isTerminal(&bytes.Buffer{}) {
		t.Error("a buffer is not a terminal")
	}
	file, err := os.Create(filepath.Join(t.TempDir(), "log"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if isTerminal(file) {
		t.Error("a regular file is not a terminal")
	}
	// /dev/null is a character device like a terminal; with it the exit is
	// not delayed, which is what an interactive run needs.
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Skip(err)
	}
	defer null.Close()
	if !isTerminal(null) {
		t.Error("a character device should count as a terminal")
	}
	started = time.Now()
	holdBeforeExit(settings{Stdout: null}, logger, time.Now())
	if waited := time.Since(started); waited > time.Second {
		t.Errorf("waited %s although the output is a terminal", waited)
	}
}

func TestDaemonRejectsBadLogSettings(t *testing.T) {
	h := newHarness(t, favoriteConfig)
	set := h.settings()
	set.LogLevel = "loud"
	if code := run(nil, set); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

// TestDaemonEndToEnd starts the real daemon, waits for a push, scrapes it
// over HTTP and stops it with SIGTERM, the way a container runtime does.
func TestDaemonEndToEnd(t *testing.T) {
	h := newHarness(t, favoriteConfig)
	now := time.Now()
	h.api.setLive(map[string]liveShow{"TechnoBase": {
		DJ: "BlueCore", ID: 525081, Show: "Happy Hardcore Bass Kick", Style: "Happy Hardcore",
		Start: now.Add(-time.Hour), End: now.Add(time.Hour),
	}})
	set := h.settings()
	set.LogLevel = "info"
	set.HTTPAddr = "127.0.0.1:0"

	exit := make(chan int, 1)
	go func() { exit <- run(nil, set) }()

	waitFor(t, "the first push", func() bool { return len(h.push.sent()) == 1 })
	waitFor(t, "the state file", func() bool { _, err := os.Stat(h.statePath); return err == nil })

	var addr string
	for _, raw := range strings.Split(h.logs.String(), "\n") {
		var line map[string]any
		if json.Unmarshal([]byte(raw), &line) == nil && line["event"] == obs.EventHTTPListening {
			addr = line["http_addr"].(string)
		}
	}
	if addr == "" {
		t.Fatalf("no http.listening line in:\n%s", h.logs.String())
	}

	get := func(path string) (int, string) {
		resp, err := http.Get("http://" + addr + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	if status, body := get("/healthz"); status != 200 || body != "ok\n" {
		t.Errorf("/healthz = %d %q", status, body)
	}
	status, body := get("/metrics")
	if status != 200 {
		t.Errorf("/metrics = %d", status)
	}
	for _, series := range []string{
		`basealert_notifications_total{kind="favorite",result="sent",station="TechnoBase"} 1`,
		`basealert_station_favorite_live{station="TechnoBase"} 1`,
		`basealert_build_info{version="` + version + `"`,
		"process_resident_memory_bytes ",
	} {
		if !strings.Contains(body, series) {
			t.Errorf("/metrics lacks %s", series)
		}
	}
	if code, out, _ := command(settings{HTTPAddr: addr}, "health"); code != 0 || out != "ok\n" {
		t.Errorf("health command against the daemon: exit %d %q", code, out)
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-exit:
		if code != 0 {
			t.Errorf("exit code after SIGTERM = %d, want 0", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not stop on SIGTERM")
	}
	if _, err := http.Get("http://" + addr + "/healthz"); err == nil {
		t.Error("HTTP server still answers after shutdown")
	}

	var order []string
	for _, line := range h.logLines() {
		order = append(order, line["event"].(string))
	}
	if got := strings.Join(order, " "); got != "app.started http.listening config.loaded station.live notification.sent app.stopping" {
		t.Errorf("log of a daemon run = %s", got)
	}
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
