package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sample = `
timezone: Europe/Berlin
poll_interval: 90s
stations: [technobase, HardBase.FM]

favorites:
  - BlueCore
  - 373169
  - "DJ   Bert S"
  - No

slots:
  - days: [Fr, sa]
    from: 20:00
    to: "02:00"
    stations: [TechnoBase]
  - from: "14:00"
    to: 18:00

notify:
  favorite: { priority: 1, sound: siren }
  slot: { priority: -1 }

outage_alert_after: 0
`

func mustParse(t *testing.T, yaml string) *Config {
	t.Helper()
	c, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return c
}

func TestParseSample(t *testing.T) {
	c := mustParse(t, sample)

	if c.Location.String() != "Europe/Berlin" {
		t.Errorf("location = %s", c.Location)
	}
	if c.PollInterval != 90*time.Second {
		t.Errorf("poll interval = %s", c.PollInterval)
	}
	if got := strings.Join(c.Stations, ","); got != "TechnoBase,HardBase" {
		t.Errorf("stations = %s", got)
	}
	if c.OutageAlertAfter != 0 {
		t.Errorf("outage alert = %s, want disabled", c.OutageAlertAfter)
	}
	if c.NotifyFavorite != (Rule{Priority: 1, Sound: "siren"}) || c.NotifySlot != (Rule{Priority: -1}) {
		t.Errorf("notify = %+v / %+v", c.NotifyFavorite, c.NotifySlot)
	}
	if len(c.Hash) != 12 {
		t.Errorf("hash = %q", c.Hash)
	}

	wantFav := []Favorite{{Name: "BlueCore"}, {ID: 373169}, {Name: "DJ Bert S"}, {Name: "No"}}
	if len(c.Favorites) != len(wantFav) {
		t.Fatalf("favorites = %+v", c.Favorites)
	}
	for i, f := range wantFav {
		if c.Favorites[i] != f {
			t.Errorf("favorites[%d] = %+v, want %+v", i, c.Favorites[i], f)
		}
	}

	if len(c.Slots) != 2 {
		t.Fatalf("slots = %+v", c.Slots)
	}
	weekend := c.Slots[0]
	if weekend.From != 20*60 || weekend.To != 2*60 {
		t.Errorf("slot 1 = %d–%d", weekend.From, weekend.To)
	}
	if !weekend.Days[time.Friday] || !weekend.Days[time.Saturday] || weekend.Days[time.Sunday] {
		t.Errorf("slot 1 days = %v", weekend.Days)
	}
	if !weekend.Covers("TechnoBase") || weekend.Covers("HardBase") {
		t.Error("slot 1 should cover TechnoBase only")
	}
	daily := c.Slots[1]
	for day, on := range daily.Days {
		if !on {
			t.Errorf("slot 2 without days should run daily, %s is off", time.Weekday(day))
		}
	}
	if !daily.Covers("HardBase") {
		t.Error("slot 2 without stations should cover every watched station")
	}
}

func TestDefaults(t *testing.T) {
	for name, yaml := range map[string]string{"empty": "", "comment only": "# nothing yet\n"} {
		t.Run(name, func(t *testing.T) {
			c := mustParse(t, yaml)
			if c.Location.String() != DefaultTimezone {
				t.Errorf("location = %s", c.Location)
			}
			if c.PollInterval != DefaultPollInterval || c.OutageAlertAfter != DefaultOutageAlertAfter {
				t.Errorf("intervals = %s / %s", c.PollInterval, c.OutageAlertAfter)
			}
			if got := strings.Join(c.Stations, ","); got != "TechnoBase,HouseTime,HardBase,TranceBase" {
				t.Errorf("stations = %s", got)
			}
			if len(c.Favorites) != 0 || len(c.Slots) != 0 {
				t.Errorf("favorites/slots should be empty")
			}
		})
	}
}

// A single value where a list is expected is a natural thing to write.
func TestSingleValueCountsAsList(t *testing.T) {
	c := mustParse(t, `
stations: TechnoBase
favorites: BlueCore
slots:
  - days: Fr
    from: "20:00"
    to: "22:00"
    stations: TechnoBase
`)
	if got := strings.Join(c.Stations, ","); got != "TechnoBase" {
		t.Errorf("stations = %s", got)
	}
	if len(c.Favorites) != 1 || c.Favorites[0].Name != "BlueCore" {
		t.Errorf("favorites = %+v", c.Favorites)
	}
	if len(c.Slots) != 1 || !c.Slots[0].Days[time.Friday] || c.Slots[0].Days[time.Saturday] || !c.Slots[0].Covers("TechnoBase") {
		t.Errorf("slots = %+v", c.Slots)
	}

	// An empty value is an empty list, which means "all" for stations and days.
	c = mustParse(t, "stations:\nfavorites:\nslots:\n  - {days: , from: '10:00', to: '11:00'}\n")
	if len(c.Stations) != 4 || len(c.Favorites) != 0 || !c.Slots[0].Days[time.Monday] {
		t.Errorf("empty lists: stations=%v favorites=%v days=%v", c.Stations, c.Favorites, c.Slots[0].Days)
	}
}

func TestIsFavorite(t *testing.T) {
	c := mustParse(t, sample)
	tests := []struct {
		name string
		id   int64
		want bool
	}{
		{"BlueCore", 525081, true},
		{"bluecore", 0, true},
		{"  BLUECORE ", 0, true},
		{"Blue Core", 0, false},
		{"FlyMatiks", 373169, true}, // by ID, the name is not listed
		{"FlyMatiks", 0, false},
		{"dj bert s", 553066, true},
		{"No", 1, true},
		{"Somebody", 12345, false},
		{"373169", 0, false}, // a numeric entry is an ID, not a name
		{"", 0, false},
	}
	for _, tt := range tests {
		if got := c.IsFavorite(tt.name, tt.id); got != tt.want {
			t.Errorf("IsFavorite(%q, %d) = %v, want %v", tt.name, tt.id, got, tt.want)
		}
	}
}

func TestSlotActiveUsesConfiguredTimezone(t *testing.T) {
	c := mustParse(t, sample)
	// Friday 2026-10-09 20:30 in Berlin is 18:30 UTC.
	at := time.Date(2026, 10, 9, 18, 30, 0, 0, time.UTC)
	if !c.SlotActive("TechnoBase", at) {
		t.Error("TechnoBase should be in the weekend slot at Fr 20:30 Berlin time")
	}
	if c.SlotActive("HardBase", at) {
		t.Error("HardBase is only covered by the 14–18 slot")
	}
	// 15:00 Berlin time on the same day.
	at = time.Date(2026, 10, 9, 13, 0, 0, 0, time.UTC)
	if !c.SlotActive("HardBase", at) || !c.SlotActive("TechnoBase", at) {
		t.Error("the daily slot should cover both stations at 15:00")
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want []string // every fragment must appear in the error
	}{
		{"unknown key", "favourites: [BlueCore]\n", []string{`Zeile 1: unbekannter Schlüssel "favourites"`}},
		{"two unknown keys", "favourites: [x]\nslot: []\n", []string{`Zeile 1: unbekannter Schlüssel "favourites"`, `Zeile 2: unbekannter Schlüssel "slot"`}},
		{"broken yaml", "favorites: [BlueCore\nslots: 3\n", []string{"Zeile"}},
		{"unknown station", "stations: [TechnoBase, CoreTime]\n", []string{"stations", "CoreTime"}},
		{"unknown timezone", "timezone: Mars/Olympus\n", []string{"timezone", "Mars/Olympus"}},
		{"poll too fast", "poll_interval: 5s\n", []string{"poll_interval", "30s"}},
		{"poll not a duration", "poll_interval: soon\n", []string{"Zeile 1", "soon", "Dauer"}},
		{"empty favorite", "favorites: [BlueCore, '']\n", []string{"favorites", "Eintrag 2"}},
		{"nested favorite", "favorites:\n  - name: BlueCore\n", []string{"Zeile 2", "einfacher Wert"}},
		{"bad day", "slots:\n  - {days: [Funday], from: '10:00', to: '11:00'}\n", []string{"Slot 1", "Funday"}},
		{"bad clock", "slots:\n  - {from: '25:00', to: '11:00'}\n", []string{"Slot 1", "from", "25:00"}},
		{"missing to", "slots:\n  - {from: '10:00'}\n", []string{"Slot 1", "to fehlt"}},
		{"from 24:00", "slots:\n  - {from: '24:00', to: '02:00'}\n", []string{"24:00"}},
		{"slot station not watched", "stations: [TechnoBase]\nslots:\n  - {from: '10:00', to: '11:00', stations: [HardBase]}\n", []string{"HardBase", "nicht beobachtet"}},
		{"priority out of range", "notify:\n  favorite: {priority: 2}\n", []string{"notify.favorite.priority"}},
		{"priority not a number", "notify:\n  favorite: {priority: hoch}\n", []string{`Zeile 2: ungültiger Wert "hoch"`}},
		{"slots not a list", "slots: 3\n", []string{`Zeile 1: ungültiger Wert "3"`}},
		{"slots as a mapping", "slots:\n  from: '10:00'\n", []string{"Zeile 2", "anderer Aufbau"}},
		{"favorites as a mapping", "favorites:\n  name: BlueCore\n", []string{"Zeile 2", "Liste erwartet"}},
		{"negative outage", "outage_alert_after: -5m\n", []string{"outage_alert_after"}},
		{"several problems at once", "stations: [Nope]\npoll_interval: 1s\n", []string{"Nope", "poll_interval"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))
			if err == nil {
				t.Fatal("expected an error")
			}
			msg := err.Error()
			if strings.Contains(msg, "\n") {
				t.Errorf("error must be a single line: %q", msg)
			}
			if strings.Contains(msg, "rawConfig") || strings.Contains(msg, "config.") || strings.Contains(msg, "line ") || strings.Contains(msg, "unmarshal") || strings.Contains(msg, "!!") {
				t.Errorf("error leaks Go type names: %q", msg)
			}
			for _, w := range tt.want {
				if !strings.Contains(msg, w) {
					t.Errorf("error %q does not mention %q", msg, w)
				}
			}
		})
	}
}

func TestLoaderDetectsChangesByContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	l := NewLoader(path)

	// A missing file is reported once, not on every poll.
	if _, _, changed, err := l.Poll(); !changed || err == nil {
		t.Fatalf("missing file: changed=%v err=%v", changed, err)
	}
	if _, _, changed, _ := l.Poll(); changed {
		t.Fatal("missing file must not be reported twice")
	}

	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write("favorites: [BlueCore]\n")
	cfg, hash, changed, err := l.Poll()
	if !changed || err != nil || cfg == nil || hash != cfg.Hash {
		t.Fatalf("first load: cfg=%v hash=%q changed=%v err=%v", cfg, hash, changed, err)
	}
	if _, _, changed, _ := l.Poll(); changed {
		t.Fatal("unchanged file reported as changed")
	}

	// Rewriting the same bytes (new mtime, same content) is not a change.
	write("favorites: [BlueCore]\n")
	if _, _, changed, _ := l.Poll(); changed {
		t.Fatal("identical content reported as changed")
	}

	write("favorites: [BlueCore\n")
	cfg, badHash, changed, err := l.Poll()
	if !changed || err == nil || cfg != nil {
		t.Fatalf("broken file: cfg=%v changed=%v err=%v", cfg, changed, err)
	}
	if badHash == "" || badHash == hash {
		t.Errorf("broken file should carry its own hash, got %q", badHash)
	}
	if _, _, changed, _ := l.Poll(); changed {
		t.Fatal("a broken file must be reported once per version")
	}

	write("favorites: [BlueCore, Orca]\n")
	cfg, _, changed, err = l.Poll()
	if !changed || err != nil || len(cfg.Favorites) != 2 {
		t.Fatalf("fixed file: cfg=%+v changed=%v err=%v", cfg, changed, err)
	}
}
