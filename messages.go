package main

import (
	"fmt"
	"strings"
	"time"

	"basealert/internal/config"
	"basealert/internal/engine"
	"basealert/internal/pushover"
)

// playerURL opens the web player; the fragment selects the station.
const playerURL = "https://weareone.fm/#"

// stationMessage builds the push for a show that is on air.
//
//	Favourite: "BlueCore ist live" / "TechnoBase.FM – Show" / "Style · 20:00–22:00 Uhr"
//	Slot:      "TechnoBase.FM ist live" / "BlueCore – Show" / "Style · bis 22:00 Uhr"
func stationMessage(n engine.Notification, cfg *config.Config, now time.Time) pushover.Message {
	station := n.Station + ".FM"
	rule := cfg.NotifySlot
	title := station + " ist live"
	headline := joinNonEmpty(" – ", n.DJ, n.Show)
	times := clockRange(time.Time{}, n.End, cfg.Location)
	if n.Kind == engine.KindFavorite {
		rule = cfg.NotifyFavorite
		title = n.DJ + " ist live"
		headline = joinNonEmpty(" – ", station, n.Show)
		times = clockRange(n.Start, n.End, cfg.Location)
	}

	msg := pushover.Message{
		Title:    title,
		Body:     joinNonEmpty("\n", headline, joinNonEmpty(" · ", n.Style, times)),
		URL:      playerURL + n.Station,
		URLTitle: "Reinhören",
		Priority: rule.Priority,
		Sound:    rule.Sound,
	}
	// Let the push disappear when the show is over.
	if remaining := n.End.Sub(now); !n.End.IsZero() && remaining > 0 {
		msg.TTL = max(remaining, time.Minute)
	}
	return msg
}

func configErrorMessage(err error, activeConfigKept bool) pushover.Message {
	consequence := "Die bisherige Konfiguration bleibt aktiv."
	if !activeConfigKept {
		consequence = "Es ist keine gültige Konfiguration aktiv, BaseAlert meldet solange nichts."
	}
	return pushover.Message{
		Title: "BaseAlert: config.yaml fehlerhaft",
		Body:  err.Error() + "\n" + consequence,
	}
}

func outageMessage(since time.Time, err error, loc *time.Location) pushover.Message {
	return pushover.Message{
		Title: "BaseAlert: Sender-API nicht erreichbar",
		Body: fmt.Sprintf("Seit %s Uhr schlägt der Abruf fehl (%s). Sendestarts werden solange nicht erkannt.",
			since.In(loc).Format("15:04"), err),
	}
}

func recoveryMessage(since, until time.Time, loc *time.Location) pushover.Message {
	return pushover.Message{
		Title: "BaseAlert: Sender-API wieder erreichbar",
		Body: fmt.Sprintf("Ausfall von %s bis %s Uhr (%s). Sendestarts in dieser Zeit wurden möglicherweise nicht gemeldet.",
			since.In(loc).Format("15:04"), until.In(loc).Format("15:04"), germanDuration(until.Sub(since))),
	}
}

// clockRange formats "20:00–22:00 Uhr", or "bis 22:00 Uhr" without a start.
func clockRange(start, end time.Time, loc *time.Location) string {
	switch {
	case end.IsZero():
		return ""
	case start.IsZero():
		return "bis " + end.In(loc).Format("15:04") + " Uhr"
	default:
		return start.In(loc).Format("15:04") + "–" + end.In(loc).Format("15:04") + " Uhr"
	}
}

func germanDuration(d time.Duration) string {
	minutes := int(d.Round(time.Minute) / time.Minute)
	if minutes < 60 {
		return fmt.Sprintf("%d min", minutes)
	}
	return fmt.Sprintf("%d h %02d min", minutes/60, minutes%60)
}

func joinNonEmpty(sep string, parts ...string) string {
	kept := parts[:0:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, sep)
}
