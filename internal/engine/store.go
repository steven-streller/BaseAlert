package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"time"
)

// heartbeat is how often the state is rewritten while nothing but the "last
// seen live" timestamps changed. It bounds how stale those can be on disk.
const heartbeat = 5 * time.Minute

const stateVersion = 1

type stateFile struct {
	Version  int                     `json:"version"`
	SavedAt  time.Time               `json:"saved_at"`
	Stations map[string]StationState `json:"stations"`
}

// Store persists the engine state, so that a restart does not repeat
// notifications. An empty path disables persistence.
type Store struct {
	path    string
	saved   State
	savedAt time.Time
}

// NewStore returns a store for the file at path.
func NewStore(path string) *Store { return &Store{path: path} }

// Load reads the state file. A missing file is an empty state. Live entries
// that cannot describe a running show any more are discarded, so that a long
// downtime does not suppress the notification for a new show by the same DJ.
func (s *Store) Load(now time.Time) (st State, restored, discarded int, err error) {
	st = State{Stations: map[string]StationState{}}
	if s.path == "" {
		return st, 0, 0, nil
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return st, 0, 0, nil
	}
	if err != nil {
		return st, 0, 0, err
	}
	var f stateFile
	if err := json.Unmarshal(data, &f); err != nil {
		return st, 0, 0, fmt.Errorf("decode %s: %w", s.path, err)
	}
	if f.Version != stateVersion {
		return st, 0, 0, fmt.Errorf("%s has unsupported version %d", s.path, f.Version)
	}
	for name, e := range f.Stations {
		if e.Live && !fresh(e, now) {
			discarded++
			continue
		}
		st.Stations[name] = e
		restored++
	}
	s.saved = State{Stations: maps.Clone(st.Stations)}
	s.savedAt = f.SavedAt
	return st, restored, discarded, nil
}

// fresh reports whether a live entry may still describe the running show:
// either the show is scheduled to be on, or it was seen live recently.
func fresh(e StationState, now time.Time) bool {
	return now.Before(e.ShowEnd.Add(Grace)) || now.Sub(e.LastLiveAt) <= Grace+heartbeat
}

// Save writes st if it differs from the last written state, or if only the
// "last seen live" timestamps moved and the heartbeat is due.
func (s *Store) Save(st State, now time.Time) (written bool, err error) {
	if s.path == "" {
		return false, nil
	}
	if !s.savedAt.IsZero() && sameExceptLastLive(s.saved, st) {
		if !anyLive(st) || now.Sub(s.savedAt) < heartbeat {
			return false, nil
		}
	}
	if err := s.Flush(st, now); err != nil {
		return false, err
	}
	return true, nil
}

// Flush writes st unconditionally. A clean shutdown uses it, so that the
// timestamps on disk are current when the next process starts.
func (s *Store) Flush(st State, now time.Time) error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(stateFile{Version: stateVersion, SavedAt: now.UTC(), Stations: st.Stations}, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(s.path, append(data, '\n')); err != nil {
		return err
	}
	s.saved = State{Stations: maps.Clone(st.Stations)}
	s.savedAt = now
	return nil
}

func sameExceptLastLive(a, b State) bool {
	if len(a.Stations) != len(b.Stations) {
		return false
	}
	for name, x := range a.Stations {
		y, ok := b.Stations[name]
		if !ok {
			return false
		}
		x.LastLiveAt, y.LastLiveAt = time.Time{}, time.Time{}
		if x.Live != y.Live || x.DJ != y.DJ || x.DJID != y.DJID ||
			!x.ShowEnd.Equal(y.ShowEnd) || !x.BlockStart.Equal(y.BlockStart) ||
			x.SessionNotified != y.SessionNotified || x.BlockNotified != y.BlockNotified ||
			x.SlotActive != y.SlotActive {
			return false
		}
	}
	return true
}

func anyLive(st State) bool {
	for _, e := range st.Stations {
		if e.Live {
			return true
		}
	}
	return false
}

// writeAtomic replaces path with data via a temporary file in the same
// directory, so a crash never leaves a half-written state file behind.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*.tmp")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}
