package engine

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"basealert/internal/config"
	"basealert/internal/wao"
)

// DJs used in the timelines. F and G are the favourites of baseConfig.
var djIDs = map[string]int64{"F": 1, "G": 2, "A": 10, "B": 11, "C": 12, "K": 13}

const baseConfig = `
stations: [TechnoBase, HardBase]
favorites: [F, 2]
slots:
  - days: [Fr]
    from: "18:00"
    to: "24:00"
    stations: [TechnoBase]
`

func mustConfig(t *testing.T, yaml string) *config.Config {
	t.Helper()
	cfg, err := config.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	return cfg
}

// clock parses "Fr 17:00" into a time in the week of Friday 2026-10-09.
func clock(t *testing.T, cfg *config.Config, s string) time.Time {
	t.Helper()
	days := map[string]int{"Do": 8, "Fr": 9, "Sa": 10, "So": 11}
	day, hm, _ := strings.Cut(s, " ")
	var h, m int
	if _, err := fmt.Sscanf(hm, "%d:%d", &h, &m); err != nil || days[day] == 0 {
		t.Fatalf("bad time %q", s)
	}
	return time.Date(2026, 10, days[day], h, m, 0, 0, cfg.Location)
}

// snapshot builds a snapshot in which the listed stations are live with the
// given DJ ("TechnoBase=F") and all other known stations play the playlist.
func snapshot(at time.Time, live ...string) wao.Snapshot {
	snap := wao.Snapshot{Stations: map[string]wao.Station{}}
	for _, name := range wao.StationNames() {
		snap.Stations[name] = wao.Station{Name: name}
	}
	for _, l := range live {
		station, dj, _ := strings.Cut(l, "=")
		snap.Stations[station] = wao.Station{
			Name: station, Live: true, DJ: dj, DJID: djIDs[dj],
			Show: dj + " Show", Style: "Hands Up",
			Start: at.Truncate(time.Hour), End: at.Truncate(time.Hour).Add(2 * time.Hour),
		}
	}
	return snap
}

type step struct {
	at       string
	live     []string
	want     string // notifications as "kind:station:dj", comma separated
	events   string // events as "kind:station:decision:reason", comma separated; "-" skips the check
	sendFail bool   // the push fails transiently, so nothing is marked
}

type timeline struct {
	t     *testing.T
	cfg   *config.Config
	state State
}

func (tl *timeline) run(steps []step) {
	tl.t.Helper()
	for _, s := range steps {
		tl.step(s)
	}
}

func (tl *timeline) step(s step) {
	tl.t.Helper()
	now := clock(tl.t, tl.cfg, s.at)
	next, events, due := Evaluate(tl.state, tl.cfg, snapshot(now, s.live...), now)
	tl.state = next

	var gotDue []string
	for _, n := range due {
		gotDue = append(gotDue, fmt.Sprintf("%s:%s:%s", n.Kind, n.Station, n.DJ))
		if !s.sendFail {
			tl.state.MarkNotified(n.Station)
		}
	}
	if got := strings.Join(gotDue, ","); got != s.want {
		tl.t.Errorf("%s: notifications = %q, want %q", s.at, got, s.want)
	}

	if s.events == "-" {
		return
	}
	var gotEvents []string
	for _, e := range events {
		reason := e.Reason
		if e.Kind == StationPlaylist || e.Kind == SlotEnded {
			gotEvents = append(gotEvents, fmt.Sprintf("%s:%s", e.Kind, e.Station))
			continue
		}
		gotEvents = append(gotEvents, fmt.Sprintf("%s:%s:%s:%s", e.Kind, e.Station, e.Decision, reason))
	}
	if got := strings.Join(gotEvents, ","); got != s.events {
		tl.t.Errorf("%s: events = %q\n%swant %q", s.at, got, strings.Repeat(" ", len(s.at)+6), s.events)
	}
}

func newTimeline(t *testing.T, yaml string) *timeline {
	return &timeline{t: t, cfg: mustConfig(t, yaml)}
}

// The example from the plan: slot Fr 18–24 on one station, F and G are favourites.
func TestPlanExample(t *testing.T) {
	newTimeline(t, baseConfig).run([]step{
		{at: "Fr 16:59", events: ""},
		{at: "Fr 17:00", live: []string{"TechnoBase=F"}, want: "favorite:TechnoBase:F",
			events: "station.live:TechnoBase:notify_favorite:"},
		{at: "Fr 17:30", live: []string{"TechnoBase=F"}, events: ""},
		{at: "Fr 18:00", live: []string{"TechnoBase=F"},
			events: "slot.started:TechnoBase:none:session_already_notified"},
		{at: "Fr 19:00", live: []string{"TechnoBase=B"},
			events: "station.live:TechnoBase:none:block_already_notified"},
		{at: "Fr 20:59", live: []string{"TechnoBase=B"}, events: ""},
		{at: "Fr 21:00", events: ""}, // playlist, still inside the grace period
		{at: "Fr 21:08", events: ""},
		{at: "Fr 21:09", events: "station.playlist:TechnoBase"},
		{at: "Fr 22:00", live: []string{"TechnoBase=C"}, want: "slot:TechnoBase:C",
			events: "station.live:TechnoBase:notify_slot:"},
		{at: "Fr 23:00", live: []string{"TechnoBase=G"}, want: "favorite:TechnoBase:G",
			events: "station.live:TechnoBase:notify_favorite:"},
		{at: "Fr 23:30", live: []string{"TechnoBase=G"}, events: ""},
		{at: "Sa 00:00", live: []string{"TechnoBase=G"}, events: "slot.ended:TechnoBase"},
	})
}

func TestSlotBeginsWhileShowIsRunning(t *testing.T) {
	newTimeline(t, baseConfig).run([]step{
		{at: "Fr 16:00", live: []string{"TechnoBase=A"},
			events: "station.live:TechnoBase:none:no_rule_matched"},
		{at: "Fr 17:59", live: []string{"TechnoBase=A"}, events: ""},
		{at: "Fr 18:00", live: []string{"TechnoBase=A"}, want: "slot:TechnoBase:A",
			events: "slot.started:TechnoBase:notify_slot:"},
		{at: "Fr 18:01", live: []string{"TechnoBase=A"}, events: ""},
	})
}

// The behaviour the user chose: one push per live block, seamless DJ changes
// stay silent, a new block after a playlist break is announced again.
func TestSlotAnnouncesEachLiveBlockOnce(t *testing.T) {
	newTimeline(t, baseConfig).run([]step{
		{at: "Fr 18:00", events: "slot.started:TechnoBase:none:not_live"},
		{at: "Fr 20:00", live: []string{"TechnoBase=A"}, want: "slot:TechnoBase:A",
			events: "station.live:TechnoBase:notify_slot:"},
		{at: "Fr 22:00", live: []string{"TechnoBase=B"},
			events: "station.live:TechnoBase:none:block_already_notified"},
		{at: "Fr 22:59", live: []string{"TechnoBase=B"}, events: ""},
		{at: "Fr 23:00", events: ""},
		{at: "Fr 23:09", events: "station.playlist:TechnoBase"},
		{at: "Fr 23:30", live: []string{"TechnoBase=C"}, want: "slot:TechnoBase:C",
			events: "station.live:TechnoBase:notify_slot:"},
	})
}

func TestFavoriteAndSlotInTheSamePollGiveOnePush(t *testing.T) {
	newTimeline(t, baseConfig).run([]step{
		{at: "Fr 18:00", live: []string{"TechnoBase=F"}, want: "favorite:TechnoBase:F",
			events: "slot.started:TechnoBase:notify_favorite:,station.live:TechnoBase:notify_favorite:"},
		{at: "Fr 18:01", live: []string{"TechnoBase=F"}, events: ""},
		// The favourite's push settled the block, so the next DJ stays silent.
		{at: "Fr 20:00", live: []string{"TechnoBase=A"},
			events: "station.live:TechnoBase:none:block_already_notified"},
	})
}

func TestShortDropoutDoesNotRepeatThePush(t *testing.T) {
	newTimeline(t, baseConfig).run([]step{
		{at: "Fr 14:00", live: []string{"TechnoBase=F"}, want: "favorite:TechnoBase:F", events: "-"},
		{at: "Fr 14:29", live: []string{"TechnoBase=F"}, events: ""},
		{at: "Fr 14:30", events: ""},
		{at: "Fr 14:38", events: ""}, // nine minutes without the DJ
		{at: "Fr 14:39", live: []string{"TechnoBase=F"}, events: ""},
		// After ten minutes the block ends, and the same DJ counts as new.
		{at: "Fr 14:59", live: []string{"TechnoBase=F"}, events: ""},
		{at: "Fr 15:00", events: ""},
		{at: "Fr 15:09", events: "station.playlist:TechnoBase"},
		{at: "Fr 15:10", live: []string{"TechnoBase=F"}, want: "favorite:TechnoBase:F",
			events: "station.live:TechnoBase:notify_favorite:"},
	})
}

func TestDropoutWithDifferentDJKeepsTheBlock(t *testing.T) {
	newTimeline(t, baseConfig).run([]step{
		{at: "Fr 20:00", live: []string{"TechnoBase=A"}, want: "slot:TechnoBase:A", events: "-"},
		{at: "Fr 20:59", live: []string{"TechnoBase=A"}, events: ""},
		{at: "Fr 21:00", events: ""},
		// B connects five minutes late: same block, so no second slot push.
		{at: "Fr 21:05", live: []string{"TechnoBase=B"},
			events: "station.live:TechnoBase:none:block_already_notified"},
		// A favourite still gets its own push.
		{at: "Fr 21:59", live: []string{"TechnoBase=B"}, events: ""},
		{at: "Fr 22:00", events: ""},
		{at: "Fr 22:05", live: []string{"TechnoBase=G"}, want: "favorite:TechnoBase:G", events: "-"},
	})
}

func TestFavoriteByID(t *testing.T) {
	// G is listed as ID 2 only. Its name is not in the config.
	tl := newTimeline(t, baseConfig)
	tl.run([]step{
		{at: "Fr 10:00", live: []string{"HardBase=G"}, want: "favorite:HardBase:G",
			events: "station.live:HardBase:notify_favorite:"},
	})
}

func TestStationsAreIndependent(t *testing.T) {
	newTimeline(t, baseConfig).run([]step{
		// The slot covers TechnoBase only, so A on HardBase is nobody's business.
		{at: "Fr 20:00", live: []string{"TechnoBase=A", "HardBase=A"}, want: "slot:TechnoBase:A",
			events: "slot.started:TechnoBase:notify_slot:,station.live:TechnoBase:notify_slot:,station.live:HardBase:none:no_rule_matched"},
		{at: "Fr 21:00", live: []string{"TechnoBase=A", "HardBase=F"}, want: "favorite:HardBase:F",
			events: "station.live:HardBase:notify_favorite:"},
	})
}

func TestUnwatchedStationIsIgnored(t *testing.T) {
	// HouseTime is not in the list of watched stations.
	tl := newTimeline(t, baseConfig)
	tl.run([]step{{at: "Fr 10:00", live: []string{"HouseTime=F"}, events: ""}})
	if _, ok := tl.state.Stations["HouseTime"]; ok {
		t.Error("unwatched station must not be tracked")
	}
}

func TestConfigChangeDuringSession(t *testing.T) {
	tl := newTimeline(t, baseConfig)
	tl.run([]step{
		{at: "Fr 10:00", live: []string{"HardBase=A"}, events: "station.live:HardBase:none:no_rule_matched"},
	})

	// A becomes a favourite while playing: the push follows on the next poll.
	tl.cfg = mustConfig(t, strings.Replace(baseConfig, "[F, 2]", "[F, 2, A]", 1))
	tl.run([]step{
		{at: "Fr 10:01", live: []string{"HardBase=A"}, want: "favorite:HardBase:A", events: ""},
		{at: "Fr 10:02", live: []string{"HardBase=A"}, events: ""},
	})

	// A slot that covers "now" is added while K is live.
	tl.cfg = mustConfig(t, baseConfig+"  - {from: '00:00', to: '24:00', stations: [TechnoBase]}\n")
	tl.run([]step{
		{at: "Fr 11:00", live: []string{"TechnoBase=K", "HardBase=A"}, want: "slot:TechnoBase:K",
			events: "slot.started:TechnoBase:notify_slot:,station.live:TechnoBase:notify_slot:"},
	})

	// A station dropped from the config loses its state.
	tl.cfg = mustConfig(t, strings.Replace(baseConfig, "[TechnoBase, HardBase]", "[TechnoBase]", 1))
	tl.run([]step{{at: "Fr 11:01", live: []string{"TechnoBase=K", "HardBase=A"}, events: "-"}})
	if _, ok := tl.state.Stations["HardBase"]; ok {
		t.Error("state of a station that is no longer watched must be dropped")
	}
}

// A favourite's session that was pushed during one slot phase must not be
// pushed again when the slot phase is switched off and on by config edits.
func TestSlotPhaseRestartDoesNotRepeatTheSession(t *testing.T) {
	tl := newTimeline(t, baseConfig)
	tl.run([]step{
		{at: "Fr 20:00", live: []string{"TechnoBase=A"}, want: "slot:TechnoBase:A", events: "-"},
	})
	noSlots := mustConfig(t, "stations: [TechnoBase, HardBase]\nfavorites: [F, 2]\n")
	withSlots := tl.cfg
	tl.cfg = noSlots
	tl.run([]step{{at: "Fr 20:01", live: []string{"TechnoBase=A"}, events: "slot.ended:TechnoBase"}})
	tl.cfg = withSlots
	tl.run([]step{
		{at: "Fr 20:02", live: []string{"TechnoBase=A"},
			events: "slot.started:TechnoBase:none:session_already_notified"},
	})
}

// A live block that spans two slot phases is announced in each of them.
func TestLiveBlockAcrossTwoSlotPhases(t *testing.T) {
	yaml := "stations: [TechnoBase]\nslots:\n  - {days: [Fr], from: '20:00', to: '24:00'}\n  - {days: [Sa], from: '10:00', to: '14:00'}\n"
	newTimeline(t, yaml).run([]step{
		{at: "Fr 20:00", live: []string{"TechnoBase=A"}, want: "slot:TechnoBase:A", events: "-"},
		{at: "Fr 22:00", live: []string{"TechnoBase=B"}, events: "station.live:TechnoBase:none:block_already_notified"},
		{at: "Sa 00:00", live: []string{"TechnoBase=B"}, events: "slot.ended:TechnoBase"},
		{at: "Sa 08:00", live: []string{"TechnoBase=K"}, events: "station.live:TechnoBase:none:no_rule_matched"},
		{at: "Sa 10:00", live: []string{"TechnoBase=K"}, want: "slot:TechnoBase:K",
			events: "slot.started:TechnoBase:notify_slot:"},
		{at: "Sa 12:00", live: []string{"TechnoBase=C"}, events: "station.live:TechnoBase:none:block_already_notified"},
	})
}

func TestAdjacentSlotsFormOnePhase(t *testing.T) {
	yaml := "stations: [TechnoBase]\nslots:\n  - {from: '18:00', to: '20:00'}\n  - {from: '20:00', to: '22:00'}\n"
	newTimeline(t, yaml).run([]step{
		{at: "Fr 19:00", live: []string{"TechnoBase=A"}, want: "slot:TechnoBase:A", events: "-"},
		{at: "Fr 20:00", live: []string{"TechnoBase=A"}, events: ""},
		{at: "Fr 21:00", live: []string{"TechnoBase=B"}, events: "station.live:TechnoBase:none:block_already_notified"},
		{at: "Fr 22:00", live: []string{"TechnoBase=B"}, events: "slot.ended:TechnoBase"},
	})
}

// A push that fails transiently stays due and is retried by the next poll.
// A push that was rejected for good is marked like a delivered one.
func TestFailedPushIsRetriedUntilMarked(t *testing.T) {
	newTimeline(t, baseConfig).run([]step{
		{at: "Fr 10:00", live: []string{"HardBase=F"}, want: "favorite:HardBase:F", sendFail: true,
			events: "station.live:HardBase:notify_favorite:"},
		{at: "Fr 10:01", live: []string{"HardBase=F"}, want: "favorite:HardBase:F", sendFail: true, events: ""},
		{at: "Fr 10:02", live: []string{"HardBase=F"}, want: "favorite:HardBase:F", events: ""},
		{at: "Fr 10:03", live: []string{"HardBase=F"}, events: ""},
	})
}

func TestPendingPushIsDroppedWhenTheShowEnds(t *testing.T) {
	newTimeline(t, baseConfig).run([]step{
		{at: "Fr 10:00", live: []string{"HardBase=F"}, want: "favorite:HardBase:F", sendFail: true, events: "-"},
		{at: "Fr 10:01", live: []string{"HardBase=A"}, events: "station.live:HardBase:none:no_rule_matched"},
	})
}

func TestMissingStationKeepsItsState(t *testing.T) {
	cfg := mustConfig(t, baseConfig)
	now := clock(t, cfg, "Fr 10:00")
	state, _, due := Evaluate(State{}, cfg, snapshot(now, "HardBase=F"), now)
	if len(due) != 1 {
		t.Fatalf("due = %v", due)
	}
	state.MarkNotified("HardBase")

	partial := wao.Snapshot{Stations: map[string]wao.Station{"TechnoBase": {Name: "TechnoBase"}}}
	state, events, due := Evaluate(state, cfg, partial, now.Add(time.Minute))
	if len(events) != 0 || len(due) != 0 {
		t.Errorf("events = %v, due = %v", events, due)
	}
	if st := state.Stations["HardBase"]; !st.Live || !st.SessionNotified {
		t.Errorf("HardBase state lost: %+v", st)
	}
}

func TestNotificationCarriesShowDetails(t *testing.T) {
	cfg := mustConfig(t, baseConfig)
	now := clock(t, cfg, "Fr 20:15")
	_, events, due := Evaluate(State{}, cfg, snapshot(now, "TechnoBase=F"), now)
	if len(due) != 1 || len(events) != 2 {
		t.Fatalf("due = %v, events = %v", due, events)
	}
	start, end := clock(t, cfg, "Fr 20:00"), clock(t, cfg, "Fr 22:00")
	got := due[0]
	if !got.Start.Equal(start) || !got.End.Equal(end) {
		t.Errorf("show times = %s – %s", got.Start, got.End)
	}
	got.Start, got.End = time.Time{}, time.Time{}
	want := Notification{Kind: KindFavorite, Station: "TechnoBase", DJ: "F", DJID: 1, Show: "F Show", Style: "Hands Up"}
	if got != want {
		t.Errorf("notification = %+v\nwant           %+v", got, want)
	}
	live := events[1]
	if live.Kind != StationLive || !live.Favorite || !live.SlotActive || !live.Live ||
		live.Show != "F Show" || !live.ShowEnd.Equal(end) {
		t.Errorf("station.live event = %+v", live)
	}
}

func TestPlaylistEventReportsBlockLength(t *testing.T) {
	cfg := mustConfig(t, baseConfig)
	tl := &timeline{t: t, cfg: cfg}
	tl.run([]step{
		{at: "Fr 10:00", live: []string{"HardBase=A"}, events: "-"},
		{at: "Fr 11:30", live: []string{"HardBase=B"}, events: "-"},
	})
	now := clock(t, cfg, "Fr 12:00")
	_, events, _ := Evaluate(tl.state, cfg, snapshot(now), now)
	if len(events) != 1 || events[0].Kind != StationPlaylist {
		t.Fatalf("events = %+v", events)
	}
	if e := events[0]; e.DJ != "B" || e.LiveSeconds != 90*60 {
		t.Errorf("playlist event = %+v, want last DJ B after 5400 s", e)
	}
}
