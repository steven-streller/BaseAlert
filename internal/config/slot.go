package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Slot is a recurring time window in which any live DJ is worth a
// notification, no matter who it is.
type Slot struct {
	Days [7]bool // indexed by time.Weekday; the day a window starts on
	From int     // minutes since midnight, 0..1439
	To   int     // minutes since midnight, 0..1440; To <= From crosses midnight

	stations map[string]bool // nil: all watched stations
}

// Covers reports whether the slot applies to station.
func (s Slot) Covers(station string) bool {
	return s.stations == nil || s.stations[station]
}

// Active reports whether the window is open at t. Only the wall clock of t is
// used, so t must already be in the location the slot was written for.
func (s Slot) Active(t time.Time) bool {
	day := t.Weekday()
	minute := t.Hour()*60 + t.Minute()
	if s.From < s.To {
		return s.Days[day] && minute >= s.From && minute < s.To
	}
	// The window crosses midnight (or lasts 24 hours when From == To): it is
	// open late on its own day and early on the day after.
	previous := (day + 6) % 7
	return (s.Days[day] && minute >= s.From) || (s.Days[previous] && minute < s.To)
}

var dayNames = map[string]time.Weekday{
	"mo": time.Monday, "mon": time.Monday, "montag": time.Monday, "monday": time.Monday,
	"di": time.Tuesday, "tue": time.Tuesday, "dienstag": time.Tuesday, "tuesday": time.Tuesday,
	"mi": time.Wednesday, "wed": time.Wednesday, "mittwoch": time.Wednesday, "wednesday": time.Wednesday,
	"do": time.Thursday, "thu": time.Thursday, "donnerstag": time.Thursday, "thursday": time.Thursday,
	"fr": time.Friday, "fri": time.Friday, "freitag": time.Friday, "friday": time.Friday,
	"sa": time.Saturday, "sat": time.Saturday, "samstag": time.Saturday, "saturday": time.Saturday,
	"so": time.Sunday, "sun": time.Sunday, "sonntag": time.Sunday, "sunday": time.Sunday,
}

func parseDay(v string) (time.Weekday, error) {
	day, ok := dayNames[strings.ToLower(strings.TrimSpace(v))]
	if !ok {
		return 0, fmt.Errorf("unbekannter Wochentag %q (erlaubt: Mo Di Mi Do Fr Sa So)", v)
	}
	return day, nil
}

// parseClock parses "HH:MM" into minutes since midnight. "24:00" is accepted
// as the end of a day.
func parseClock(v string) (int, error) {
	bad := fmt.Errorf("%q ist keine Uhrzeit im Format HH:MM", v)
	hh, mm, ok := strings.Cut(strings.TrimSpace(v), ":")
	if !ok || len(hh) == 0 || len(hh) > 2 || len(mm) != 2 {
		return 0, bad
	}
	h, err := strconv.Atoi(hh)
	if err != nil {
		return 0, bad
	}
	m, err := strconv.Atoi(mm)
	if err != nil {
		return 0, bad
	}
	if h < 0 || m < 0 || m > 59 || h > 24 || (h == 24 && m != 0) {
		return 0, bad
	}
	return h*60 + m, nil
}
