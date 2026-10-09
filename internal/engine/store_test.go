package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRestartDoesNotRepeatThePush(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	tl := newTimeline(t, baseConfig)
	tl.run([]step{
		{at: "Fr 20:00", live: []string{"TechnoBase=F"}, want: "favorite:TechnoBase:F", events: "-"},
	})
	if written, err := NewStore(path).Save(tl.state, clock(t, tl.cfg, "Fr 20:00")); err != nil || !written {
		t.Fatalf("Save: written=%v err=%v", written, err)
	}

	// The process restarts half an hour into the show.
	restart := clock(t, tl.cfg, "Fr 20:30")
	state, restored, discarded, err := NewStore(path).Load(restart)
	if err != nil || restored != 2 || discarded != 0 {
		t.Fatalf("Load: restored=%d discarded=%d err=%v", restored, discarded, err)
	}
	after := &timeline{t: t, cfg: tl.cfg, state: state}
	after.run([]step{
		{at: "Fr 20:30", live: []string{"TechnoBase=F"}, events: ""},
		// The slot's block is settled as well, so the next DJ stays silent.
		{at: "Fr 22:00", live: []string{"TechnoBase=A"}, events: "station.live:TechnoBase:none:block_already_notified"},
	})
}

func TestStaleStateIsDiscarded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	tl := newTimeline(t, baseConfig)
	tl.run([]step{
		{at: "Fr 10:00", live: []string{"HardBase=F"}, want: "favorite:HardBase:F", events: "-"},
	})
	if _, err := NewStore(path).Save(tl.state, clock(t, tl.cfg, "Fr 10:00")); err != nil {
		t.Fatal(err)
	}

	// The show ran until 12:00. A day later the same DJ is on again, which
	// is a new session that deserves a new push.
	restart := clock(t, tl.cfg, "Sa 10:00")
	state, restored, discarded, err := NewStore(path).Load(restart)
	if err != nil || restored != 1 || discarded != 1 {
		t.Fatalf("Load: restored=%d discarded=%d err=%v", restored, discarded, err)
	}
	after := &timeline{t: t, cfg: tl.cfg, state: state}
	after.run([]step{
		{at: "Sa 10:00", live: []string{"HardBase=F"}, want: "favorite:HardBase:F",
			events: "station.live:HardBase:notify_favorite:"},
	})
}

func TestStateSurvivesAnOverrunningShow(t *testing.T) {
	// The show was scheduled until 12:00 but the DJ is still on at 12:20.
	// The heartbeat keeps "last seen live" fresh enough to bridge a restart.
	path := filepath.Join(t.TempDir(), "state.json")
	tl := newTimeline(t, baseConfig)
	store := NewStore(path)
	tl.run([]step{{at: "Fr 10:00", live: []string{"HardBase=F"}, want: "favorite:HardBase:F", events: "-"}})
	if _, err := store.Save(tl.state, clock(t, tl.cfg, "Fr 10:00")); err != nil {
		t.Fatal(err)
	}
	st := tl.state.Stations["HardBase"]
	st.LastLiveAt = clock(t, tl.cfg, "Fr 12:20")
	tl.state.Stations["HardBase"] = st
	if written, err := store.Save(tl.state, clock(t, tl.cfg, "Fr 12:20")); err != nil || !written {
		t.Fatalf("heartbeat save: written=%v err=%v", written, err)
	}

	_, restored, discarded, err := NewStore(path).Load(clock(t, tl.cfg, "Fr 12:25"))
	if err != nil || restored != 2 || discarded != 0 {
		t.Errorf("Load: restored=%d discarded=%d err=%v", restored, discarded, err)
	}
}

func TestSaveWritesOnlyWhenNeeded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store := NewStore(path)
	cfg := mustConfig(t, baseConfig)
	at := func(s string) time.Time { return clock(t, cfg, s) }

	live := State{Stations: map[string]StationState{
		"TechnoBase": {Live: true, DJ: "F", DJID: 1, LastLiveAt: at("Fr 20:00"), ShowEnd: at("Fr 22:00")},
	}}
	seen := func(s string) State {
		st := live.Stations["TechnoBase"]
		st.LastLiveAt = at(s)
		return State{Stations: map[string]StationState{"TechnoBase": st}}
	}

	steps := []struct {
		name  string
		state State
		at    string
		want  bool
	}{
		{"first save", live, "Fr 20:00", true},
		{"nothing changed", seen("Fr 20:01"), "Fr 20:01", false},
		{"only last-seen moved, heartbeat not due", seen("Fr 20:04"), "Fr 20:04", false},
		{"heartbeat due", seen("Fr 20:05"), "Fr 20:05", true},
		{"heartbeat just written", seen("Fr 20:06"), "Fr 20:06", false},
	}
	for _, s := range steps {
		written, err := store.Save(s.state, at(s.at))
		if err != nil || written != s.want {
			t.Errorf("%s: written=%v err=%v, want %v", s.name, written, err, s.want)
		}
	}

	notified := seen("Fr 20:07")
	notified.MarkNotified("TechnoBase")
	if written, _ := store.Save(notified, at("Fr 20:07")); !written {
		t.Error("a changed flag must be written at once")
	}

	idle := State{Stations: map[string]StationState{"TechnoBase": {}}}
	if written, _ := store.Save(idle, at("Fr 22:10")); !written {
		t.Error("the end of the block must be written")
	}
	if written, _ := store.Save(idle, at("Sa 09:00")); written {
		t.Error("an idle state needs no heartbeat")
	}

	// No temporary files are left behind.
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 || entries[0].Name() != "state.json" {
		t.Errorf("directory content = %v", entries)
	}
}

func TestFlushAlwaysWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store := NewStore(path)
	cfg := mustConfig(t, baseConfig)
	state := State{Stations: map[string]StationState{
		"TechnoBase": {Live: true, DJ: "F", DJID: 1, LastLiveAt: clock(t, cfg, "Fr 20:00"), ShowEnd: clock(t, cfg, "Fr 22:00")},
	}}
	if _, err := store.Save(state, clock(t, cfg, "Fr 20:00")); err != nil {
		t.Fatal(err)
	}

	// A minute later only "last seen" moved, which Save would skip. A clean
	// shutdown must still leave the current timestamp on disk.
	st := state.Stations["TechnoBase"]
	st.LastLiveAt = clock(t, cfg, "Fr 20:01")
	state.Stations["TechnoBase"] = st
	if written, _ := store.Save(state, clock(t, cfg, "Fr 20:01")); written {
		t.Fatal("test setup: Save should have skipped this write")
	}
	if err := store.Flush(state, clock(t, cfg, "Fr 20:01")); err != nil {
		t.Fatal(err)
	}
	loaded, _, _, err := NewStore(path).Load(clock(t, cfg, "Fr 20:02"))
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Stations["TechnoBase"].LastLiveAt; !got.Equal(clock(t, cfg, "Fr 20:01")) {
		t.Errorf("last seen on disk = %s, want 20:01", got)
	}

	if err := NewStore("").Flush(state, clock(t, cfg, "Fr 20:01")); err != nil {
		t.Errorf("Flush without a path must be a no-op, got %v", err)
	}
}

func TestLoadEdgeCases(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 9, 18, 0, 0, 0, time.UTC)

	t.Run("no path means no persistence", func(t *testing.T) {
		store := NewStore("")
		st, restored, _, err := store.Load(now)
		if err != nil || restored != 0 || st.Stations == nil {
			t.Errorf("Load: st=%+v restored=%d err=%v", st, restored, err)
		}
		if written, err := store.Save(State{}, now); written || err != nil {
			t.Errorf("Save: written=%v err=%v", written, err)
		}
	})

	t.Run("missing file is an empty state", func(t *testing.T) {
		st, restored, _, err := NewStore(filepath.Join(dir, "none.json")).Load(now)
		if err != nil || restored != 0 || st.Stations == nil {
			t.Errorf("Load: st=%+v restored=%d err=%v", st, restored, err)
		}
	})

	for name, content := range map[string]string{
		"corrupt file":    `{"version": 1, "stations": {`,
		"unknown version": `{"version": 99, "stations": {}}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			st, _, _, err := NewStore(path).Load(now)
			if err == nil {
				t.Error("expected an error")
			}
			if st.Stations == nil || len(st.Stations) != 0 {
				t.Errorf("a broken file must yield a usable empty state, got %+v", st)
			}
		})
	}

	t.Run("unwritable directory is an error, not a panic", func(t *testing.T) {
		store := NewStore(filepath.Join(dir, "missing-dir", "state.json"))
		if written, err := store.Save(State{Stations: map[string]StationState{}}, now); written || err == nil {
			t.Errorf("Save: written=%v err=%v", written, err)
		}
	})
}
