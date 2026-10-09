package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"basealert/internal/config"
	"basealert/internal/engine"
	"basealert/internal/obs"
	"basealert/internal/pushover"
	"basealert/internal/wao"
)

// app is the daemon: it polls the station API, lets the engine decide and
// turns the engine's answers into pushes, log lines and metrics.
type app struct {
	log    *slog.Logger
	met    *obs.Metrics
	api    *wao.Client
	push   *pushover.Client
	loader *config.Loader
	store  *engine.Store
	clock  func() time.Time
	beat   obs.Heartbeat

	cfg   *config.Config // nil until a valid config has been loaded
	state engine.State

	failures      int // consecutive failed polls
	failingSince  time.Time
	outageAlerted bool // the outage was reported, so a recovery is owed

	// pending holds notifications about BaseAlert itself that still have to
	// be delivered, by kind. A newer message of a kind replaces the older.
	pending map[string]pushover.Message

	saveFailing bool
}

// systemKinds is the order in which pending notifications are sent.
var systemKinds = []string{obs.KindConfigError, obs.KindOutage, obs.KindRecovery}

func newApp(set settings, logger *slog.Logger, met *obs.Metrics, clock func() time.Time) *app {
	return &app{
		log:     logger,
		met:     met,
		api:     wao.New(set.APIURL, userAgent()),
		push:    pushover.New(set.Token, set.User, set.Device, set.PushoverURL, userAgent()),
		loader:  config.NewLoader(set.ConfigPath),
		store:   engine.NewStore(set.StatePath),
		clock:   clock,
		state:   engine.State{Stations: map[string]engine.StationState{}},
		pending: map[string]pushover.Message{},
	}
}

// minFailedLifetime is how long a daemon that is about to exit with an error
// stays alive, counted from its start. Not every restart policy backs off:
// podman restarts at once, which turns a broken setup into several starts per
// second. That floods the logs and, if the failure comes after the first
// poll, the station API as well.
var minFailedLifetime = 30 * time.Second

// runDaemon is the default command. It returns the process exit code.
func runDaemon(set settings) int {
	started := time.Now()
	logger, err := obs.NewLogger(set.Stdout, set.LogFormat, set.LogLevel)
	if err != nil {
		fmt.Fprintln(set.Stderr, "basealert:", err)
		holdBeforeExit(set, nil, started)
		return 2
	}
	code := daemon(set, logger)
	if code != 0 {
		holdBeforeExit(set, logger, started)
	}
	return code
}

// holdBeforeExit waits until the process has lived for minFailedLifetime. It
// returns at once on SIGINT or SIGTERM, and when a person is watching: in a
// terminal a delayed exit only gets in the way.
func holdBeforeExit(set settings, logger *slog.Logger, started time.Time) {
	remaining := minFailedLifetime - time.Since(started)
	if remaining <= 0 || isTerminal(set.Stdout) {
		return
	}
	if logger != nil {
		obs.Log(logger, slog.LevelInfo, obs.EventAppExitDelayed, "delaying the exit so that a restart policy cannot loop tightly",
			slog.Int64("delay_seconds", int64(remaining.Round(time.Second)/time.Second)))
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	select {
	case <-ctx.Done():
	case <-time.After(remaining):
	}
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// daemon runs until it is told to stop or cannot go on.
func daemon(set settings, logger *slog.Logger) (code int) {
	slog.SetDefault(logger)
	obs.CaptureStdLog(logger)

	// A panic would otherwise print a multi-line trace that is not JSON.
	defer func() {
		if r := recover(); r != nil {
			obs.Log(logger, slog.LevelError, obs.EventAppPanic, "panic",
				slog.String("error", fmt.Sprint(r)), slog.String("stack", string(debug.Stack())))
			code = 2
		}
	}()

	if set.Token == "" || set.User == "" {
		obs.Log(logger, slog.LevelError, obs.EventAppStopping, "PUSHOVER_TOKEN and PUSHOVER_USER must be set",
			slog.String("reason", "missing_credentials"))
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	a := newApp(set, logger, obs.NewMetrics(version), time.Now)
	a.beat.Beat(time.Now(), 3*config.DefaultPollInterval)
	obs.Log(logger, slog.LevelInfo, obs.EventAppStarted, "BaseAlert started",
		slog.String("version", version),
		slog.String("config_path", set.ConfigPath),
		slog.String("state_path", set.StatePath),
		slog.String("http_addr", set.HTTPAddr))

	var srv *http.Server
	if set.HTTPAddr != "" {
		listener, err := net.Listen("tcp", set.HTTPAddr)
		if err != nil {
			obs.Log(logger, slog.LevelError, obs.EventAppStopping, "cannot listen for HTTP",
				slog.String("reason", "listen_failed"), slog.String("http_addr", set.HTTPAddr), slog.String("error", err.Error()))
			return 1
		}
		srv = obs.NewServer(set.HTTPAddr, a.met, func() bool { return a.beat.Healthy(time.Now()) },
			obs.StdLogger(logger, obs.EventHTTPError))
		go func() {
			if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				obs.Log(logger, slog.LevelError, obs.EventHTTPError, "HTTP server stopped", slog.String("error", err.Error()))
			}
		}()
		obs.Log(logger, slog.LevelInfo, obs.EventHTTPListening, "serving /metrics and /healthz",
			slog.String("http_addr", listener.Addr().String()))
	}

	a.restoreState()
	a.loop(ctx)

	obs.Log(logger, slog.LevelInfo, obs.EventAppStopping, "BaseAlert stopping", slog.String("reason", "signal"))
	if err := a.store.Flush(a.state, a.clock()); err != nil && !a.saveFailing {
		obs.Log(logger, slog.LevelWarn, obs.EventStateSaveFailed, "cannot write state file, a restart may repeat notifications",
			slog.String("error", err.Error()))
	}
	if srv != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}
	return 0
}

// loop runs poll cycles until ctx is cancelled.
func (a *app) loop(ctx context.Context) {
	for {
		a.cycle(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(a.pollInterval()):
		}
	}
}

func (a *app) pollInterval() time.Duration {
	if a.cfg == nil {
		return config.DefaultPollInterval
	}
	return a.cfg.PollInterval
}

// cycle is one iteration: reload the config if it changed, poll, decide,
// notify, persist.
func (a *app) cycle(ctx context.Context) {
	a.reloadConfig()
	if a.cfg != nil {
		a.poll(ctx)
	}
	a.sendPending(ctx)
	a.beat.Beat(time.Now(), 3*a.pollInterval())
}

func (a *app) restoreState() {
	state, restored, discarded, err := a.store.Load(a.clock())
	a.state = state
	switch {
	case err != nil:
		obs.Log(a.log, slog.LevelWarn, obs.EventStateLoadFailed, "state file unusable, starting without memory",
			slog.String("error", err.Error()))
	case restored+discarded > 0:
		obs.Log(a.log, slog.LevelInfo, obs.EventStateRestored, "state restored",
			slog.Int("stations", restored), slog.Int("discarded", discarded))
	}
}

func (a *app) reloadConfig() {
	cfg, hash, changed, err := a.loader.Poll()
	if !changed {
		return
	}
	if err != nil {
		a.met.ConfigRejected()
		obs.Log(a.log, slog.LevelError, obs.EventConfigInvalid, "config file rejected",
			slog.String("config_path", a.loader.Path()),
			slog.String("config_hash", hash),
			slog.String("error", err.Error()),
			slog.Bool("active_config_kept", a.cfg != nil))
		a.pending[obs.KindConfigError] = configErrorMessage(err, a.cfg != nil)
		return
	}

	reload := a.cfg != nil
	a.cfg = cfg
	delete(a.pending, obs.KindConfigError)
	a.met.ConfigLoaded(len(cfg.Favorites), len(cfg.Slots))
	a.met.WatchStations(cfg.Stations)
	obs.Log(a.log, slog.LevelInfo, obs.EventConfigLoaded, "config loaded",
		slog.String("config_hash", cfg.Hash),
		slog.String("stations", strings.Join(cfg.Stations, ",")),
		slog.Int("favorites", len(cfg.Favorites)),
		slog.Int("slots", len(cfg.Slots)),
		slog.String("poll_interval", cfg.PollInterval.String()),
		slog.String("timezone", cfg.Location.String()),
		slog.Bool("reload", reload))
}

func (a *app) poll(ctx context.Context) {
	started := time.Now()
	snap, err := a.api.Radio(ctx)
	if err == nil {
		err = snap.Check(a.cfg.Stations)
	}
	elapsed := time.Since(started)
	now := a.clock()

	if err != nil {
		if ctx.Err() != nil {
			return // shutting down, not an outage
		}
		a.met.ObservePoll(wao.ErrorKind(err), elapsed)
		a.pollFailed(now, err, elapsed)
		return
	}
	a.met.ObservePoll(obs.ResultOK, elapsed)
	a.pollSucceeded(now)

	next, events, due := engine.Evaluate(a.state, a.cfg, snap, now)
	a.state = next
	for _, e := range events {
		a.logEvent(e)
	}
	for _, n := range due {
		a.notify(ctx, n, now)
	}

	live := 0
	gauges := make([]obs.StationGauge, 0, len(a.cfg.Stations))
	for _, name := range a.cfg.Stations {
		st := snap.Stations[name]
		if st.Live {
			live++
		}
		gauges = append(gauges, obs.StationGauge{
			Station:      name,
			Live:         st.Live,
			FavoriteLive: st.Live && a.cfg.IsFavorite(st.DJ, st.DJID),
			SlotActive:   a.cfg.SlotActive(name, now),
		})
	}
	a.met.PublishStations(gauges, 3*a.cfg.PollInterval)
	a.saveState(now)

	obs.Log(a.log, slog.LevelDebug, obs.EventPollCompleted, "poll completed",
		slog.Int64("duration_ms", elapsed.Milliseconds()), slog.Int("live_stations", live))
}

func (a *app) pollFailed(now time.Time, err error, elapsed time.Duration) {
	if a.failures == 0 {
		a.failingSince = now
	}
	a.failures++

	attrs := []slog.Attr{
		slog.String("error", err.Error()),
		slog.String("error_kind", wao.ErrorKind(err)),
		slog.Int("consecutive_failures", a.failures),
		slog.Int64("duration_ms", elapsed.Milliseconds()),
	}
	if status := wao.ErrorStatus(err); status != 0 {
		attrs = append(attrs, slog.Int("http_status", status))
	}
	obs.Log(a.log, slog.LevelWarn, obs.EventPollFailed, "poll of the station API failed", attrs...)

	// The outage is always logged; the push is optional.
	threshold := a.cfg.OutageAlertAfter
	if threshold == 0 {
		threshold = config.DefaultOutageAlertAfter
	}
	if a.outageAlerted || now.Sub(a.failingSince) < threshold {
		return
	}
	a.outageAlerted = true
	obs.Log(a.log, slog.LevelError, obs.EventUpstreamOutage, "station API unreachable",
		slog.Time("since", a.failingSince),
		slog.Int("consecutive_failures", a.failures),
		slog.String("error", err.Error()))
	if a.cfg.OutageAlertAfter > 0 {
		a.pending[obs.KindOutage] = outageMessage(a.failingSince, err, a.cfg.Location)
	}
}

func (a *app) pollSucceeded(now time.Time) {
	if a.failures == 0 {
		return
	}
	obs.Log(a.log, slog.LevelInfo, obs.EventUpstreamRecovered, "station API reachable again",
		slog.Time("since", a.failingSince),
		slog.Int64("outage_seconds", int64(now.Sub(a.failingSince)/time.Second)),
		slog.Int("failed_polls", a.failures))
	if a.outageAlerted && a.cfg.OutageAlertAfter > 0 {
		// If the outage push never got through, the recovery tells the
		// whole story on its own.
		delete(a.pending, obs.KindOutage)
		a.pending[obs.KindRecovery] = recoveryMessage(a.failingSince, now, a.cfg.Location)
	}
	a.failures, a.failingSince, a.outageAlerted = 0, time.Time{}, false
}

// notify sends the push for a show. A delivered or rejected push settles the
// notification; after a transient failure it stays due and the next cycle
// tries again, as long as the show is still on.
func (a *app) notify(ctx context.Context, n engine.Notification, now time.Time) {
	kind := string(n.Kind)
	attrs := []slog.Attr{
		slog.String("kind", kind),
		slog.String("station", n.Station),
		slog.String("dj", n.DJ),
		slog.Int64("dj_id", n.DJID),
		slog.String("show", n.Show),
	}
	result := a.send(ctx, kind, n.Station, stationMessage(n, a.cfg, now), attrs)
	if result != obs.ResultFailed {
		a.state.MarkNotified(n.Station)
	}
}

// sendPending delivers notifications about BaseAlert itself.
func (a *app) sendPending(ctx context.Context) {
	for _, kind := range systemKinds {
		msg, ok := a.pending[kind]
		if !ok {
			continue
		}
		if a.send(ctx, kind, "", msg, []slog.Attr{slog.String("kind", kind)}) != obs.ResultFailed {
			delete(a.pending, kind)
		}
	}
}

// send delivers one push and reports the outcome as a log line and a metric.
func (a *app) send(ctx context.Context, kind, station string, msg pushover.Message, attrs []slog.Attr) (result string) {
	started := time.Now()
	receipt, err := a.push.Send(ctx, msg)
	attrs = append(attrs, slog.Int64("duration_ms", time.Since(started).Milliseconds()))

	var perr *pushover.Error
	switch {
	case err == nil:
		result = obs.ResultSent
		obs.Log(a.log, slog.LevelInfo, obs.EventNotificationSent, "push sent",
			append(attrs, slog.String("title", msg.Title), slog.String("pushover_request", receipt.Request))...)
	case errors.As(err, &perr) && perr.Permanent:
		result = obs.ResultRejected
		obs.Log(a.log, slog.LevelError, obs.EventNotificationRejected, "push rejected, giving up",
			append(attrs, slog.String("error", err.Error()), slog.Int("http_status", perr.Status))...)
	default:
		result = obs.ResultFailed
		if perr != nil && perr.Status != 0 {
			attrs = append(attrs, slog.Int("http_status", perr.Status))
		}
		obs.Log(a.log, slog.LevelWarn, obs.EventNotificationFailed, "push failed, will retry",
			append(attrs, slog.String("error", err.Error()))...)
	}
	a.met.CountNotification(kind, result, station)
	return result
}

func (a *app) saveState(now time.Time) {
	if _, err := a.store.Save(a.state, now); err != nil {
		// Report once, not every minute.
		if !a.saveFailing {
			obs.Log(a.log, slog.LevelWarn, obs.EventStateSaveFailed, "cannot write state file, a restart may repeat notifications",
				slog.String("error", err.Error()))
		}
		a.saveFailing = true
		return
	}
	a.saveFailing = false
}

// logEvent writes the engine's account of a state change, including the
// decision and its reason. These lines answer "why was there (no) push?".
func (a *app) logEvent(e engine.Event) {
	decision := []slog.Attr{slog.String("decision", string(e.Decision))}
	if e.Reason != "" {
		decision = append(decision, slog.String("reason", string(e.Reason)))
	}

	switch e.Kind {
	case engine.StationLive:
		attrs := []slog.Attr{
			slog.String("station", e.Station),
			slog.String("dj", e.DJ),
			slog.Int64("dj_id", e.DJID),
			slog.String("show", e.Show),
			slog.String("style", e.Style),
		}
		if !e.ShowStart.IsZero() {
			attrs = append(attrs, slog.Time("show_start", e.ShowStart))
		}
		if !e.ShowEnd.IsZero() {
			attrs = append(attrs, slog.Time("show_end", e.ShowEnd))
		}
		attrs = append(attrs, slog.Bool("favorite", e.Favorite), slog.Bool("slot_active", e.SlotActive))
		obs.Log(a.log, slog.LevelInfo, obs.EventStationLive, "DJ session started", append(attrs, decision...)...)

	case engine.StationPlaylist:
		obs.Log(a.log, slog.LevelInfo, obs.EventStationPlaylist, "live block ended",
			slog.String("station", e.Station),
			slog.String("dj", e.DJ),
			slog.Int64("dj_id", e.DJID),
			slog.Int64("live_seconds", e.LiveSeconds))

	case engine.SlotStarted:
		attrs := []slog.Attr{slog.String("station", e.Station), slog.Bool("live", e.Live)}
		if e.Live {
			attrs = append(attrs, slog.String("dj", e.DJ), slog.Int64("dj_id", e.DJID), slog.Bool("favorite", e.Favorite))
		}
		obs.Log(a.log, slog.LevelInfo, obs.EventSlotStarted, "slot phase started", append(attrs, decision...)...)

	case engine.SlotEnded:
		obs.Log(a.log, slog.LevelInfo, obs.EventSlotEnded, "slot phase ended",
			slog.String("station", e.Station), slog.Bool("live", e.Live))
	}
}
