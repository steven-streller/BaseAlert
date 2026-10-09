package config

import (
	"testing"
	"time"
)

func berlin(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func slot(from, to string, days ...time.Weekday) Slot {
	s := Slot{}
	s.From, _ = parseClock(from)
	s.To, _ = parseClock(to)
	for _, d := range days {
		s.Days[d] = true
	}
	return s
}

func TestSlotActive(t *testing.T) {
	loc := berlin(t)
	// 2026-10-09 is a Friday.
	at := func(day, hour, minute int) time.Time {
		return time.Date(2026, 10, day, hour, minute, 0, 0, loc)
	}
	evening := slot("18:00", "24:00", time.Friday)
	afternoon := slot("14:00", "18:00", time.Friday)
	overnight := slot("20:00", "02:00", time.Friday, time.Saturday)
	allDay := slot("00:00", "00:00", time.Sunday)
	fromSix := slot("06:00", "06:00", time.Friday)

	tests := []struct {
		name string
		slot Slot
		at   time.Time
		want bool
	}{
		{"before start", evening, at(9, 17, 59), false},
		{"start is inclusive", evening, at(9, 18, 0), true},
		{"last minute before 24:00", evening, at(9, 23, 59), true},
		{"24:00 ends at midnight", evening, at(10, 0, 0), false},
		{"wrong weekday", evening, at(10, 19, 0), false},
		{"same day: last minute", afternoon, at(9, 17, 59), true},
		{"same day: end is exclusive", afternoon, at(9, 18, 0), false},

		{"overnight: evening of the start day", overnight, at(9, 20, 0), true},
		{"overnight: after midnight belongs to Friday's window", overnight, at(10, 1, 59), true},
		{"overnight: end is exclusive", overnight, at(10, 2, 0), false},
		{"overnight: Saturday afternoon is closed", overnight, at(10, 15, 0), false},
		{"overnight: Saturday evening", overnight, at(10, 23, 0), true},
		{"overnight: Sunday 01:00 belongs to Saturday's window", overnight, at(11, 1, 0), true},
		{"overnight: Sunday evening is not a start day", overnight, at(11, 21, 0), false},
		{"overnight: Friday 01:00 has no Thursday window", overnight, at(9, 1, 0), false},

		{"from == to at midnight covers the whole day", allDay, at(11, 0, 0), true},
		{"whole day, late", allDay, at(11, 23, 59), true},
		{"whole day ends with the day", allDay, at(12, 0, 0), false},
		{"from == to runs 24 hours from the start", fromSix, at(9, 6, 0), true},
		{"24 hours: next morning still open", fromSix, at(10, 5, 59), true},
		{"24 hours: closes at the start time", fromSix, at(10, 6, 0), false},
		{"24 hours: not yet open", fromSix, at(9, 5, 59), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.slot.Active(tt.at); got != tt.want {
				t.Errorf("Active(%s) = %v, want %v", tt.at.Format("Mon 15:04"), got, tt.want)
			}
		})
	}
}

// On 2026-10-25 Berlin switches back to winter time: 03:00 CEST becomes 02:00
// CET, so the hour from 02:00 to 03:00 happens twice. A slot follows the wall
// clock and is simply one hour longer that night.
func TestSlotActiveAcrossEndOfSummerTime(t *testing.T) {
	loc := berlin(t)
	night := slot("22:00", "04:00", time.Saturday)

	first := time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC)  // 02:30 CEST
	second := time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC) // 02:30 CET
	end := time.Date(2026, 10, 25, 3, 0, 0, 0, time.UTC)     // 04:00 CET

	for _, at := range []time.Time{first, second} {
		local := at.In(loc)
		if local.Format("15:04") != "02:30" {
			t.Fatalf("test setup: %s is %s in Berlin", at, local.Format("15:04 MST"))
		}
		if !night.Active(local) {
			t.Errorf("slot should be open at %s", local.Format("15:04 MST"))
		}
	}
	if night.Active(end.In(loc)) {
		t.Error("slot should close at 04:00 CET")
	}
	if got := end.Sub(time.Date(2026, 10, 24, 22, 0, 0, 0, loc)); got != 7*time.Hour {
		t.Errorf("the window should last 7 hours that night, got %s", got)
	}
}

func TestParseClock(t *testing.T) {
	good := map[string]int{"00:00": 0, "7:05": 425, "07:05": 425, "20:00": 1200, "23:59": 1439, "24:00": 1440, " 12:30 ": 750}
	for in, want := range good {
		if got, err := parseClock(in); err != nil || got != want {
			t.Errorf("parseClock(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "20", "20:0", "20:000", "24:01", "25:00", "12:60", "ab:cd", "-1:00", "1200", "12:30:00"} {
		if got, err := parseClock(in); err == nil {
			t.Errorf("parseClock(%q) = %d, want an error", in, got)
		}
	}
}

func TestParseDay(t *testing.T) {
	good := map[string]time.Weekday{"Mo": time.Monday, "di": time.Tuesday, "MI": time.Wednesday, "Do": time.Thursday,
		"Fr": time.Friday, "Sa": time.Saturday, "So": time.Sunday, "Mon": time.Monday, "sunday": time.Sunday, "Samstag": time.Saturday}
	for in, want := range good {
		if got, err := parseDay(in); err != nil || got != want {
			t.Errorf("parseDay(%q) = %s, %v; want %s", in, got, err, want)
		}
	}
	if _, err := parseDay("Funday"); err == nil {
		t.Error("parseDay(Funday) should fail")
	}
}
