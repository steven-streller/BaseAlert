// Package wao reads the public player API of the WeAreOne.FM network
// (api.tb-group.fm). The API is undocumented, so decoding is deliberately
// tolerant and every response is checked for plausibility.
package wao

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the API the weareone.fm web player talks to.
const DefaultBaseURL = "https://api.tb-group.fm"

// maxBody caps how much of a response is read. /v1/radio is below 1 KiB.
const maxBody = 1 << 20

// StationInfo describes one station of the network.
type StationInfo struct {
	Name string // key used by /v1/radio, e.g. "TechnoBase"
	ID   int    // numeric id used by /v1/showplan
}

// Stations lists the stations of the network in display order.
var Stations = []StationInfo{
	{Name: "TechnoBase", ID: 5},
	{Name: "HouseTime", ID: 6},
	{Name: "HardBase", ID: 7},
	{Name: "TranceBase", ID: 8},
}

// StationNames returns the names of all known stations in display order.
func StationNames() []string {
	names := make([]string, len(Stations))
	for i, s := range Stations {
		names[i] = s.Name
	}
	return names
}

// CanonicalStation maps user input such as "technobase" or "HardBase.FM" to
// the name the API uses.
func CanonicalStation(name string) (string, bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.TrimSuffix(n, ".fm")
	for _, s := range Stations {
		if strings.ToLower(s.Name) == n {
			return s.Name, true
		}
	}
	return "", false
}

// Error kinds, used as the "result" label of the poll counter.
const (
	KindTimeout    = "timeout"
	KindNetwork    = "network"
	KindHTTPStatus = "http_status"
	KindInvalid    = "invalid_response"
)

// Error is a failed API call together with its classification.
type Error struct {
	Kind   string
	Status int // HTTP status, 0 if no response was received
	Err    error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// ErrorKind returns the classification of err, or KindNetwork for errors that
// did not originate in this package.
func ErrorKind(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return KindNetwork
}

// ErrorStatus returns the HTTP status attached to err, or 0.
func ErrorStatus(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Status
	}
	return 0
}

// Station is the live status of one station.
type Station struct {
	Name  string
	Live  bool // false: the playlist is running
	DJ    string
	DJID  int64
	Show  string
	Style string
	Start time.Time
	End   time.Time

	// malformed marks an entry that is neither a playlist nor a live show,
	// which hints at a changed API format.
	malformed bool
}

// Snapshot is the live status of all stations at one point in time.
type Snapshot struct {
	Stations map[string]Station
}

// Check reports an error if one of the named stations is missing from the
// snapshot or could not be interpreted.
func (s Snapshot) Check(names []string) error {
	for _, name := range names {
		st, ok := s.Stations[name]
		if !ok {
			return &Error{Kind: KindInvalid, Err: fmt.Errorf("station %s missing in response", name)}
		}
		if st.malformed {
			return &Error{Kind: KindInvalid, Err: fmt.Errorf("station %s has neither a DJ nor the playlist flag", name)}
		}
	}
	return nil
}

// Show is one entry of a station's schedule.
type Show struct {
	Name  string
	DJ    string
	DJID  int64
	Style string
	Start time.Time
	End   time.Time
}

// Client is an API client. The zero value is not usable, use New.
type Client struct {
	baseURL   string
	userAgent string
	http      *http.Client
}

// New returns a client for baseURL. An empty baseURL selects DefaultBaseURL.
func New(baseURL, userAgent string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		userAgent: userAgent,
		http:      &http.Client{Timeout: 10 * time.Second},
	}
}

type rawStation struct {
	DJ       string   `json:"dj"`
	ID       flexInt  `json:"id"`
	Show     string   `json:"sn"`
	Style    string   `json:"ss"`
	Start    flexInt  `json:"st"`
	End      flexInt  `json:"en"`
	Playlist flexBool `json:"p"`
}

// Radio fetches the live status of all stations.
func (c *Client) Radio(ctx context.Context) (Snapshot, error) {
	var raw map[string]rawStation
	if err := c.getJSON(ctx, "/v1/radio", &raw); err != nil {
		return Snapshot{}, err
	}
	if len(raw) == 0 {
		return Snapshot{}, &Error{Kind: KindInvalid, Err: errors.New("response contains no stations")}
	}
	snap := Snapshot{Stations: make(map[string]Station, len(raw))}
	for name, r := range raw {
		st := Station{Name: name}
		dj := strings.TrimSpace(r.DJ)
		switch {
		case bool(r.Playlist):
			// The web player ignores every other field once "p" is set.
		case dj != "":
			st.Live = true
			st.DJ = dj
			st.DJID = int64(r.ID)
			st.Show = strings.TrimSpace(r.Show)
			st.Style = strings.TrimSpace(r.Style)
			st.Start = msTime(int64(r.Start))
			st.End = msTime(int64(r.End))
		default:
			st.malformed = true
		}
		snap.Stations[name] = st
	}
	return snap, nil
}

type rawShow struct {
	Name  string  `json:"n"`
	DJ    string  `json:"m"`
	DJID  flexInt `json:"mi"`
	Style string  `json:"ss"`
	Start flexInt `json:"s"`
	End   flexInt `json:"e"`
}

// ShowplanDays is the number of days /v1/showplan serves: day 0 is yesterday,
// day 1 today, day 8 a week from today.
const ShowplanDays = 9

// Showplan fetches the schedule of one station for one day.
func (c *Client) Showplan(ctx context.Context, stationID, day int) ([]Show, error) {
	var raw []rawShow
	if err := c.getJSON(ctx, fmt.Sprintf("/v1/showplan/%d/%d", stationID, day), &raw); err != nil {
		return nil, err
	}
	shows := make([]Show, 0, len(raw))
	for _, r := range raw {
		shows = append(shows, Show{
			Name:  strings.TrimSpace(r.Name),
			DJ:    strings.TrimSpace(r.DJ),
			DJID:  int64(r.DJID),
			Style: strings.TrimSpace(r.Style),
			Start: msTime(int64(r.Start)),
			End:   msTime(int64(r.End)),
		})
	}
	return shows, nil
}

func (c *Client) getJSON(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return &Error{Kind: KindNetwork, Err: err}
	}
	req.Header.Set("Accept", "application/json")
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return transportError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Drain a little so the connection can be reused.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return &Error{Kind: KindHTTPStatus, Status: resp.StatusCode, Err: fmt.Errorf("unexpected status %s", resp.Status)}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return transportError(err)
	}
	if len(body) > maxBody {
		return &Error{Kind: KindInvalid, Status: resp.StatusCode, Err: fmt.Errorf("response larger than %d bytes", maxBody)}
	}
	if err := json.Unmarshal(body, v); err != nil {
		return &Error{Kind: KindInvalid, Status: resp.StatusCode, Err: fmt.Errorf("decode response: %w", err)}
	}
	return nil
}

func transportError(err error) error {
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return &Error{Kind: KindTimeout, Err: err}
	}
	return &Error{Kind: KindNetwork, Err: err}
}

func msTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// flexInt decodes a JSON number or a string holding a number. The web player
// runs parseInt over these fields, so the API has served both forms.
type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		*f = flexInt(n)
		return nil
	}
	fl, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("not a number: %s", b)
	}
	*f = flexInt(fl)
	return nil
}

// flexBool decodes true, "true", 1 and "1" as true and everything else as false.
type flexBool bool

func (f *flexBool) UnmarshalJSON(b []byte) error {
	s := strings.ToLower(strings.Trim(strings.TrimSpace(string(b)), `"`))
	*f = s == "true" || s == "1"
	return nil
}
