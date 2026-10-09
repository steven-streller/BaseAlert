// Package config loads and validates config.yaml: the watched stations, the
// favourite DJs and the time slots. Validation messages are German because
// they are shown to the user in a push notification.
package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"basealert/internal/wao"
)

const (
	DefaultTimezone         = "Europe/Berlin"
	DefaultPollInterval     = 60 * time.Second
	MinPollInterval         = 30 * time.Second
	DefaultOutageAlertAfter = 15 * time.Minute
)

// Rule holds the Pushover settings for one kind of notification.
type Rule struct {
	Priority int
	Sound    string
}

// Favorite is one entry of the favourites list.
type Favorite struct {
	Name string // as written in the file; empty for an ID entry
	ID   int64  // 0 for a name entry
}

// Config is a validated configuration.
type Config struct {
	Location         *time.Location
	PollInterval     time.Duration
	Stations         []string // names as the API uses them, in network order
	Favorites        []Favorite
	Slots            []Slot
	NotifyFavorite   Rule
	NotifySlot       Rule
	OutageAlertAfter time.Duration // 0 disables the outage alert
	Hash             string        // short hash of the file content, for logs

	favNames map[string]bool
	favIDs   map[int64]bool
}

// IsFavorite reports whether the DJ is on the favourites list, by ID or by
// name. Names are compared without regard to case and spacing.
func (c *Config) IsFavorite(name string, id int64) bool {
	if id != 0 && c.favIDs[id] {
		return true
	}
	return c.favNames[fold(name)]
}

// SlotActive reports whether at least one slot is open for station at t.
func (c *Config) SlotActive(station string, t time.Time) bool {
	local := t.In(c.Location)
	for _, s := range c.Slots {
		if s.Covers(station) && s.Active(local) {
			return true
		}
	}
	return false
}

// Watches reports whether station is on the list of watched stations.
func (c *Config) Watches(station string) bool {
	for _, s := range c.Stations {
		if s == station {
			return true
		}
	}
	return false
}

func fold(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(name), " "))
}

// scalar keeps the literal text of a YAML value, so that 20:00, 373169 or a
// DJ called "No" are not reinterpreted by YAML's type resolution.
type scalar string

func (s *scalar) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("Zeile %d: hier wird ein einfacher Wert erwartet", n.Line)
	}
	if n.Tag == "!!null" {
		*s = ""
		return nil
	}
	*s = scalar(n.Value)
	return nil
}

// list is a YAML sequence of plain values. A single plain value counts as a
// list of one, so "days: Fr" means the same as "days: [Fr]".
type list []scalar

func (l *list) UnmarshalYAML(n *yaml.Node) error {
	var items []*yaml.Node
	switch {
	case n.Kind == yaml.SequenceNode:
		items = n.Content
	case n.Kind == yaml.ScalarNode && n.Tag == "!!null":
		// An empty value is an empty list.
	case n.Kind == yaml.ScalarNode:
		items = []*yaml.Node{n}
	default:
		return fmt.Errorf("Zeile %d: hier wird eine Liste erwartet", n.Line)
	}
	out := make(list, 0, len(items))
	for _, item := range items {
		var s scalar
		if err := s.UnmarshalYAML(item); err != nil {
			return err
		}
		out = append(out, s)
	}
	*l = out
	return nil
}

type duration time.Duration

func (d *duration) UnmarshalYAML(n *yaml.Node) error {
	v := strings.TrimSpace(n.Value)
	if n.Kind == yaml.ScalarNode && v == "0" {
		*d = 0
		return nil
	}
	parsed, err := time.ParseDuration(v)
	if n.Kind != yaml.ScalarNode || err != nil {
		return fmt.Errorf("Zeile %d: %q ist keine Dauer wie 60s oder 15m", n.Line, n.Value)
	}
	*d = duration(parsed)
	return nil
}

type rawConfig struct {
	Timezone         scalar    `yaml:"timezone"`
	PollInterval     *duration `yaml:"poll_interval"`
	Stations         list      `yaml:"stations"`
	Favorites        list      `yaml:"favorites"`
	Slots            []rawSlot `yaml:"slots"`
	Notify           rawNotify `yaml:"notify"`
	OutageAlertAfter *duration `yaml:"outage_alert_after"`
}

type rawSlot struct {
	Days     list   `yaml:"days"`
	From     scalar `yaml:"from"`
	To       scalar `yaml:"to"`
	Stations list   `yaml:"stations"`
}

type rawNotify struct {
	Favorite rawRule `yaml:"favorite"`
	Slot     rawRule `yaml:"slot"`
}

type rawRule struct {
	Priority int    `yaml:"priority"`
	Sound    scalar `yaml:"sound"`
}

// Parse validates the content of a config file. All problems found are
// reported together.
func Parse(data []byte) (*Config, error) {
	var raw rawConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil && !errors.Is(err, io.EOF) {
		return nil, cleanYAMLError(err)
	}

	sum := sha256.Sum256(data)
	c := &Config{
		PollInterval:     DefaultPollInterval,
		OutageAlertAfter: DefaultOutageAlertAfter,
		Hash:             hex.EncodeToString(sum[:])[:12],
		favNames:         map[string]bool{},
		favIDs:           map[int64]bool{},
	}
	var problems []string
	fail := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	tz := strings.TrimSpace(string(raw.Timezone))
	if tz == "" {
		tz = DefaultTimezone
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		fail("timezone: unbekannte Zeitzone %q", tz)
		loc = time.UTC
	}
	c.Location = loc

	if raw.PollInterval != nil {
		c.PollInterval = time.Duration(*raw.PollInterval)
		if c.PollInterval < MinPollInterval {
			fail("poll_interval: mindestens %s, angegeben %s", MinPollInterval, c.PollInterval)
		}
	}
	if raw.OutageAlertAfter != nil {
		c.OutageAlertAfter = time.Duration(*raw.OutageAlertAfter)
		if c.OutageAlertAfter < 0 {
			fail("outage_alert_after: darf nicht negativ sein")
		}
	}

	watched := map[string]bool{}
	for _, s := range raw.Stations {
		name, ok := wao.CanonicalStation(string(s))
		if !ok {
			fail("stations: unbekannter Sender %q (bekannt: %s)", string(s), strings.Join(wao.StationNames(), ", "))
			continue
		}
		watched[name] = true
	}
	for _, name := range wao.StationNames() {
		if len(raw.Stations) == 0 || watched[name] {
			watched[name] = true
			c.Stations = append(c.Stations, name)
		}
	}

	for i, f := range raw.Favorites {
		v := strings.Join(strings.Fields(string(f)), " ")
		if v == "" {
			fail("favorites: Eintrag %d ist leer", i+1)
			continue
		}
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
			c.Favorites = append(c.Favorites, Favorite{ID: id})
			c.favIDs[id] = true
			continue
		}
		c.Favorites = append(c.Favorites, Favorite{Name: v})
		c.favNames[fold(v)] = true
	}

	for i, rs := range raw.Slots {
		slot, errs := parseSlot(rs, watched)
		for _, e := range errs {
			fail("slots: Slot %d: %s", i+1, e)
		}
		if len(errs) == 0 {
			c.Slots = append(c.Slots, slot)
		}
	}

	c.NotifyFavorite = parseRule("notify.favorite", raw.Notify.Favorite, fail)
	c.NotifySlot = parseRule("notify.slot", raw.Notify.Slot, fail)

	if len(problems) > 0 {
		return nil, errors.New(strings.Join(problems, "; "))
	}
	return c, nil
}

func parseSlot(rs rawSlot, watched map[string]bool) (Slot, []string) {
	var slot Slot
	var errs []string

	if len(rs.Days) == 0 {
		for i := range slot.Days {
			slot.Days[i] = true
		}
	}
	for _, d := range rs.Days {
		day, err := parseDay(string(d))
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		slot.Days[day] = true
	}

	from, err := parseClock(string(rs.From))
	switch {
	case rs.From == "":
		errs = append(errs, "from fehlt")
	case err != nil:
		errs = append(errs, "from: "+err.Error())
	case from == 24*60:
		errs = append(errs, "from: 24:00 ist nur als Ende erlaubt")
	}
	to, err := parseClock(string(rs.To))
	switch {
	case rs.To == "":
		errs = append(errs, "to fehlt")
	case err != nil:
		errs = append(errs, "to: "+err.Error())
	}
	slot.From, slot.To = from, to

	for _, s := range rs.Stations {
		name, ok := wao.CanonicalStation(string(s))
		switch {
		case !ok:
			errs = append(errs, fmt.Sprintf("unbekannter Sender %q", string(s)))
		case !watched[name]:
			errs = append(errs, fmt.Sprintf("Sender %s steht nicht unter stations und wird nicht beobachtet", name))
		default:
			if slot.stations == nil {
				slot.stations = map[string]bool{}
			}
			slot.stations[name] = true
		}
	}
	return slot, errs
}

func parseRule(key string, r rawRule, fail func(string, ...any)) Rule {
	// Priority 2 (emergency) needs retry and expire parameters and is not supported.
	if r.Priority < -2 || r.Priority > 1 {
		fail("%s.priority: erlaubt sind -2 bis 1, angegeben %d", key, r.Priority)
	}
	return Rule{Priority: r.Priority, Sound: strings.TrimSpace(string(r.Sound))}
}

var (
	goTypeInError = regexp.MustCompile(` in type \S+`)
	unknownField  = regexp.MustCompile(`field (\S+) not found`)
	wrongValue    = regexp.MustCompile("cannot unmarshal !!\\w+ `([^`]*)` into \\S+")
	wrongShape    = regexp.MustCompile(`cannot unmarshal !!\w+ into \S+`)
	lineNumber    = regexp.MustCompile(`\bline (\d+):`)
)

// cleanYAMLError turns the multi-line errors of the YAML library into one
// line, drops the Go type names they mention and translates the parts whose
// wording is known.
func cleanYAMLError(err error) error {
	msg := goTypeInError.ReplaceAllString(err.Error(), "")
	msg = strings.Join(strings.Fields(msg), " ")
	msg = strings.TrimPrefix(msg, "yaml: ")
	msg = strings.TrimPrefix(msg, "unmarshal errors: ")
	msg = unknownField.ReplaceAllString(msg, `unbekannter Schlüssel "$1"`)
	msg = wrongValue.ReplaceAllString(msg, `ungültiger Wert "$1"`)
	msg = wrongShape.ReplaceAllString(msg, "an dieser Stelle wird ein anderer Aufbau erwartet")
	msg = lineNumber.ReplaceAllString(msg, "Zeile $1:")
	return errors.New(msg)
}

// Loader re-reads a config file and detects changes by content, which also
// works when an editor or a Kubernetes ConfigMap mount replaces the file.
type Loader struct {
	path string
	seen string
	read bool
}

// NewLoader returns a loader for the file at path.
func NewLoader(path string) *Loader { return &Loader{path: path} }

// Path returns the file the loader reads.
func (l *Loader) Path() string { return l.path }

// Poll reads the file. changed is false if the content is the same as on the
// previous call; cfg and err are then nil. On a change, either cfg or err is
// set. hash identifies the content and is empty if the file is unreadable.
func (l *Loader) Poll() (cfg *Config, hash string, changed bool, err error) {
	data, readErr := os.ReadFile(l.path)
	fingerprint := "content:" + string(data)
	if readErr != nil {
		fingerprint = "error:" + readErr.Error()
	}
	if l.read && fingerprint == l.seen {
		return nil, "", false, nil
	}
	l.read, l.seen = true, fingerprint
	if readErr != nil {
		return nil, "", true, fmt.Errorf("Datei nicht lesbar: %w", readErr)
	}
	cfg, err = Parse(data)
	if err != nil {
		sum := sha256.Sum256(data)
		return nil, hex.EncodeToString(sum[:])[:12], true, err
	}
	return cfg, cfg.Hash, true, nil
}
