// Package pushover sends notifications through the Pushover message API
// (https://pushover.net/api).
package pushover

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// DefaultEndpoint is the message endpoint of the Pushover API.
const DefaultEndpoint = "https://api.pushover.net/1/messages.json"

// Limits of the API, in characters.
const (
	maxTitle    = 250
	maxMessage  = 1024
	maxURL      = 512
	maxURLTitle = 100
)

// Message is one push notification.
type Message struct {
	Title    string
	Body     string
	URL      string
	URLTitle string
	Priority int           // -2..1; emergency priority (2) is not supported
	Sound    string        // empty: the user's default sound
	TTL      time.Duration // 0: the message does not expire
}

// Receipt is Pushover's acknowledgement of an accepted message.
type Receipt struct {
	Request string // id of the request, to quote when contacting Pushover's support
}

// Error is a failed send. Permanent failures must not be retried: Pushover
// blocks clients that keep repeating rejected requests.
type Error struct {
	Permanent bool
	Status    int      // HTTP status, 0 if no response was received
	Messages  []string // error texts from the API
	Err       error    // transport error, if any
}

func (e *Error) Error() string {
	switch {
	case e.Err != nil:
		return "pushover: " + e.Err.Error()
	case len(e.Messages) > 0:
		return fmt.Sprintf("pushover: HTTP %d: %s", e.Status, strings.Join(e.Messages, "; "))
	default:
		return fmt.Sprintf("pushover: HTTP %d", e.Status)
	}
}

func (e *Error) Unwrap() error { return e.Err }

// Client sends messages for one application token to one user or group key.
type Client struct {
	token     string
	user      string
	device    string
	endpoint  string
	userAgent string
	http      *http.Client
}

// New returns a client. device may be empty to reach all of the user's
// devices; an empty endpoint selects DefaultEndpoint.
func New(token, user, device, endpoint, userAgent string) *Client {
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	return &Client{
		token:     token,
		user:      user,
		device:    device,
		endpoint:  endpoint,
		userAgent: userAgent,
		http:      &http.Client{Timeout: 15 * time.Second},
	}
}

// Send delivers m. A nil error means Pushover accepted and queued the message.
func (c *Client) Send(ctx context.Context, m Message) (Receipt, error) {
	form := url.Values{}
	form.Set("token", c.token)
	form.Set("user", c.user)
	form.Set("message", truncate(m.Body, maxMessage))
	if m.Title != "" {
		form.Set("title", truncate(m.Title, maxTitle))
	}
	if c.device != "" {
		form.Set("device", c.device)
	}
	if m.URL != "" && utf8.RuneCountInString(m.URL) <= maxURL {
		form.Set("url", m.URL)
		if m.URLTitle != "" {
			form.Set("url_title", truncate(m.URLTitle, maxURLTitle))
		}
	}
	if m.Priority != 0 {
		form.Set("priority", strconv.Itoa(m.Priority))
	}
	if m.Sound != "" {
		form.Set("sound", m.Sound)
	}
	if ttl := int64(m.TTL / time.Second); ttl > 0 {
		form.Set("ttl", strconv.FormatInt(ttl, 10))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Receipt{}, &Error{Permanent: true, Err: err}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return Receipt{}, &Error{Err: err}
	}
	defer resp.Body.Close()

	var body struct {
		Status  int      `json:"status"`
		Request string   `json:"request"`
		Errors  []string `json:"errors"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	_ = json.Unmarshal(raw, &body)

	switch {
	case resp.StatusCode == http.StatusOK && body.Status == 1:
		return Receipt{Request: body.Request}, nil
	case resp.StatusCode >= 400 && resp.StatusCode < 500, resp.StatusCode == http.StatusOK:
		// Invalid input, wrong credentials or an exhausted quota (429):
		// repeating the same request cannot succeed.
		return Receipt{Request: body.Request}, &Error{Permanent: true, Status: resp.StatusCode, Messages: body.Errors}
	default:
		return Receipt{Request: body.Request}, &Error{Status: resp.StatusCode, Messages: body.Errors}
	}
}

// truncate shortens s to at most max characters, ending in an ellipsis.
func truncate(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max-1]) + "…"
}
