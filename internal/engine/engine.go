// Package engine decides when a notification is due. It performs no I/O:
// Evaluate maps the previous state, the configuration and a snapshot of the
// stations to the next state and returns what happened, and why, as data.
// The caller turns that data into pushes, log lines and metrics.
//
// Vocabulary:
//
//   - DJ session: the same DJ is live on a station without interruption.
//   - Live block: a station is live without interruption, by any number of DJs.
//   - Slot phase: at least one configured time slot is open for a station.
//
// Rules:
//
//  1. A favourite's DJ session is announced once, always.
//  2. A live block is announced once per slot phase.
//  3. A session is never announced twice. If the running session was already
//     announced as a favourite, the block counts as announced for the slot.
package engine

import (
	"strings"
	"time"

	"basealert/internal/config"
	"basealert/internal/wao"
)

// Grace is how long a station may fall back to the playlist without ending
// the DJ session and the live block. It absorbs a DJ's connection dropping.
const Grace = 10 * time.Minute

// Decision is the outcome of evaluating the rules for one station.
type Decision string

const (
	NotifyFavorite Decision = "notify_favorite"
	NotifySlot     Decision = "notify_slot"
	NoNotification Decision = "none"
)

// Reason explains a NoNotification decision.
type Reason string

const (
	ReasonNoRule                 Reason = "no_rule_matched"
	ReasonSessionAlreadyNotified Reason = "session_already_notified"
	ReasonBlockAlreadyNotified   Reason = "block_already_notified"
	ReasonNotLive                Reason = "not_live"
)

// EventKind names a state change. The values are the "event" field of the
// corresponding log lines.
type EventKind string

const (
	StationLive     EventKind = "station.live"     // a DJ session began
	StationPlaylist EventKind = "station.playlist" // a live block ended
	SlotStarted     EventKind = "slot.started"     // a slot phase began
	SlotEnded       EventKind = "slot.ended"       // a slot phase ended
)

// Event records a state change of one station together with the decision
// that was in effect for the station at that moment.
type Event struct {
	Kind        EventKind
	Station     string
	Live        bool
	DJ          string
	DJID        int64
	Show        string
	Style       string
	ShowStart   time.Time
	ShowEnd     time.Time
	Favorite    bool
	SlotActive  bool
	Decision    Decision
	Reason      Reason // set if Decision is NoNotification
	LiveSeconds int64  // StationPlaylist: length of the block that ended
}

// Kind tells which rule asked for a notification.
type Kind string

const (
	KindFavorite Kind = "favorite"
	KindSlot     Kind = "slot"
)

// Notification is a push that is due. It stays due on every evaluation until
// State.MarkNotified is called, which is how a failed send gets retried.
type Notification struct {
	Kind    Kind
	Station string
	DJ      string
	DJID    int64
	Show    string
	Style   string
	Start   time.Time
	End     time.Time
}

// StationState is what the engine remembers about one station.
type StationState struct {
	Live            bool      `json:"live"`
	DJ              string    `json:"dj,omitempty"`
	DJID            int64     `json:"dj_id,omitempty"`
	ShowEnd         time.Time `json:"show_end,omitzero"`
	BlockStart      time.Time `json:"block_start,omitzero"`
	LastLiveAt      time.Time `json:"last_live_at,omitzero"`
	SessionNotified bool      `json:"session_notified"` // the listener knows about the running DJ session
	BlockNotified   bool      `json:"block_notified"`   // the live block was announced in the running slot phase
	SlotActive      bool      `json:"slot_active"`
}

// State is the engine's memory. The zero value is an empty state.
type State struct {
	Stations map[string]StationState `json:"stations"`
}

// MarkNotified records that the listener has been told about the DJ session
// running on station. During a slot phase this also settles the live block.
func (s *State) MarkNotified(station string) {
	st, ok := s.Stations[station]
	if !ok || !st.Live {
		return
	}
	st.SessionNotified = true
	if st.SlotActive {
		st.BlockNotified = true
	}
	s.Stations[station] = st
}

// Evaluate applies snap to prev. Stations that are not watched are dropped
// from the state; stations missing from snap keep their state unchanged.
func Evaluate(prev State, cfg *config.Config, snap wao.Snapshot, now time.Time) (State, []Event, []Notification) {
	next := State{Stations: make(map[string]StationState, len(cfg.Stations))}
	var events []Event
	var due []Notification

	for _, name := range cfg.Stations {
		st := prev.Stations[name]
		cur, ok := snap.Stations[name]
		if !ok {
			next.Stations[name] = st
			continue
		}

		inSlot := cfg.SlotActive(name, now)
		slotStarted := inSlot && !st.SlotActive
		slotEnded := !inSlot && st.SlotActive

		if !cur.Live {
			// Within the grace period the block is kept open, in case the
			// DJ only lost the connection.
			if st.Live && now.Sub(st.LastLiveAt) >= Grace {
				events = append(events, Event{
					Kind:        StationPlaylist,
					Station:     name,
					DJ:          st.DJ,
					DJID:        st.DJID,
					SlotActive:  inSlot,
					Decision:    NoNotification,
					Reason:      ReasonNotLive,
					LiveSeconds: int64(st.LastLiveAt.Sub(st.BlockStart) / time.Second),
				})
				st = StationState{}
			}
			st.SlotActive = inSlot
			if !inSlot {
				st.BlockNotified = false
			}
			if slotStarted || slotEnded {
				kind := SlotStarted
				if slotEnded {
					kind = SlotEnded
				}
				events = append(events, Event{Kind: kind, Station: name, SlotActive: inSlot, Decision: NoNotification, Reason: ReasonNotLive})
			}
			next.Stations[name] = st
			continue
		}

		newBlock := !st.Live
		if newBlock {
			st = StationState{Live: true, BlockStart: now}
		}
		newSession := newBlock || !sameDJ(st, cur)
		if newSession {
			st.SessionNotified = false
		}
		st.DJ, st.DJID = cur.DJ, cur.DJID
		st.ShowEnd = cur.End
		st.LastLiveAt = now
		st.SlotActive = inSlot
		if !inSlot {
			// Arm the slot rule for the next slot phase.
			st.BlockNotified = false
		}

		favorite := cfg.IsFavorite(cur.DJ, cur.DJID)
		decision, reason := NoNotification, ReasonNoRule
		switch {
		case favorite && !st.SessionNotified:
			decision, reason = NotifyFavorite, ""
		case inSlot && !st.BlockNotified && !st.SessionNotified:
			decision, reason = NotifySlot, ""
		case inSlot && !st.BlockNotified:
			// The listener already knows about this session, so the block
			// counts as announced without a second push.
			st.BlockNotified = true
			reason = ReasonSessionAlreadyNotified
		case favorite:
			reason = ReasonSessionAlreadyNotified
		case inSlot:
			reason = ReasonBlockAlreadyNotified
		}

		if decision != NoNotification {
			kind := KindFavorite
			if decision == NotifySlot {
				kind = KindSlot
			}
			due = append(due, Notification{
				Kind: kind, Station: name,
				DJ: cur.DJ, DJID: cur.DJID, Show: cur.Show, Style: cur.Style,
				Start: cur.Start, End: cur.End,
			})
		}

		event := Event{
			Station: name, Live: true,
			DJ: cur.DJ, DJID: cur.DJID, Show: cur.Show, Style: cur.Style,
			ShowStart: cur.Start, ShowEnd: cur.End,
			Favorite: favorite, SlotActive: inSlot,
			Decision: decision, Reason: reason,
		}
		if slotStarted || slotEnded {
			event.Kind = SlotStarted
			if slotEnded {
				event.Kind = SlotEnded
			}
			events = append(events, event)
		}
		if newSession {
			event.Kind = StationLive
			events = append(events, event)
		}
		next.Stations[name] = st
	}
	return next, events, due
}

// sameDJ compares by ID and falls back to the name if an ID is missing.
func sameDJ(st StationState, cur wao.Station) bool {
	if st.DJID != 0 && cur.DJID != 0 {
		return st.DJID == cur.DJID
	}
	return strings.EqualFold(st.DJ, cur.DJ)
}
