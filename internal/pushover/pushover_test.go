package pushover

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// fake is a stand-in for the Pushover API that records the last request.
type fake struct {
	srv    *httptest.Server
	status int
	body   string
	form   url.Values
	header http.Header
	method string
}

func newFake(t *testing.T, status int, body string) *fake {
	t.Helper()
	f := &fake{status: status, body: body}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.form, f.header, f.method = r.PostForm, r.Header, r.Method
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.status)
		_, _ = w.Write([]byte(f.body))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func TestSendEncodesTheMessage(t *testing.T) {
	f := newFake(t, 200, `{"status":1,"request":"647d2300-702c-4b38-8b2f-d56326ae460b"}`)
	c := New("app-token", "user-key", "phone", f.srv.URL, "BaseAlert/test")

	receipt, err := c.Send(context.Background(), Message{
		Title:    "BlueCore ist live",
		Body:     "TechnoBase.FM – Happy Hardcore Bass Kick\nHappy Hardcore · 20:00–22:00 Uhr",
		URL:      "https://weareone.fm/#TechnoBase",
		URLTitle: "Reinhören",
		Priority: 1,
		Sound:    "siren",
		TTL:      90 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Request != "647d2300-702c-4b38-8b2f-d56326ae460b" {
		t.Errorf("request id = %q", receipt.Request)
	}
	if f.method != http.MethodPost {
		t.Errorf("method = %s", f.method)
	}
	if got := f.header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
		t.Errorf("content type = %q", got)
	}
	if got := f.header.Get("User-Agent"); got != "BaseAlert/test" {
		t.Errorf("user agent = %q", got)
	}
	want := map[string]string{
		"token":     "app-token",
		"user":      "user-key",
		"device":    "phone",
		"title":     "BlueCore ist live",
		"message":   "TechnoBase.FM – Happy Hardcore Bass Kick\nHappy Hardcore · 20:00–22:00 Uhr",
		"url":       "https://weareone.fm/#TechnoBase",
		"url_title": "Reinhören",
		"priority":  "1",
		"sound":     "siren",
		"ttl":       "5400",
	}
	for key, value := range want {
		if got := f.form.Get(key); got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}
	if len(f.form) != len(want) {
		t.Errorf("unexpected extra fields: %v", f.form)
	}
}

func TestSendOmitsDefaults(t *testing.T) {
	f := newFake(t, 200, `{"status":1,"request":"r"}`)
	if _, err := New("t", "u", "", f.srv.URL, "").Send(context.Background(), Message{Body: "hello"}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"title", "device", "url", "url_title", "priority", "sound", "ttl"} {
		if f.form.Has(key) {
			t.Errorf("%s should not be sent, got %q", key, f.form.Get(key))
		}
	}
	if f.form.Get("message") != "hello" {
		t.Errorf("message = %q", f.form.Get("message"))
	}
}

func TestSendTruncatesToTheAPILimits(t *testing.T) {
	f := newFake(t, 200, `{"status":1,"request":"r"}`)
	_, err := New("t", "u", "", f.srv.URL, "").Send(context.Background(), Message{
		Title: strings.Repeat("ä", 300),
		Body:  strings.Repeat("ö", 2000),
	})
	if err != nil {
		t.Fatal(err)
	}
	for key, limit := range map[string]int{"title": 250, "message": 1024} {
		got := f.form.Get(key)
		if n := utf8.RuneCountInString(got); n != limit {
			t.Errorf("%s has %d characters, want %d", key, n, limit)
		}
		if !utf8.ValidString(got) || !strings.HasSuffix(got, "…") {
			t.Errorf("%s was not cut cleanly: …%q", key, got[len(got)-8:])
		}
	}
}

func TestSendClassifiesFailures(t *testing.T) {
	tests := []struct {
		name          string
		status        int
		body          string
		wantPermanent bool
		wantText      string
	}{
		{"invalid token", 400, `{"token":"invalid","errors":["application token is invalid"],"status":0,"request":"r1"}`, true, "application token is invalid"},
		{"quota exhausted", 429, `{"status":0,"errors":["application has exceeded its monthly quota"],"request":"r2"}`, true, "monthly quota"},
		{"accepted status but API says no", 200, `{"status":0,"errors":["user key is invalid"]}`, true, "user key is invalid"},
		{"server error", 500, `{"status":0}`, false, "HTTP 500"},
		{"bad gateway with html", 502, `<html>Bad Gateway</html>`, false, "HTTP 502"},
		{"unexpected redirect status", 302, ``, false, "HTTP 302"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake(t, tt.status, tt.body)
			_, err := New("t", "u", "", f.srv.URL, "").Send(context.Background(), Message{Body: "x"})
			var perr *Error
			if !errors.As(err, &perr) {
				t.Fatalf("err = %v, want *Error", err)
			}
			if perr.Permanent != tt.wantPermanent {
				t.Errorf("permanent = %v, want %v", perr.Permanent, tt.wantPermanent)
			}
			if perr.Status != tt.status {
				t.Errorf("status = %d, want %d", perr.Status, tt.status)
			}
			if !strings.Contains(err.Error(), tt.wantText) {
				t.Errorf("error %q does not mention %q", err, tt.wantText)
			}
		})
	}
}

func TestSendTransportErrorIsTransientAndKeepsSecretsOut(t *testing.T) {
	f := newFake(t, 200, `{"status":1}`)
	endpoint := f.srv.URL
	f.srv.Close()

	_, err := New("secret-app-token", "secret-user-key", "", endpoint, "").Send(context.Background(), Message{Body: "x"})
	var perr *Error
	if !errors.As(err, &perr) {
		t.Fatalf("err = %v, want *Error", err)
	}
	if perr.Permanent || perr.Status != 0 {
		t.Errorf("a refused connection must be transient, got %+v", perr)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Errorf("error text leaks credentials: %q", err)
	}
}
