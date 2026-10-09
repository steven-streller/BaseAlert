package wao

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func serve(t *testing.T, status int, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, "BaseAlert/test")
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRadioFixture(t *testing.T) {
	var gotPath, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotUA = r.URL.Path, r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(fixture(t, "radio.json")))
	}))
	defer srv.Close()

	// A trailing slash in the base URL must not produce "//v1/radio".
	snap, err := New(srv.URL+"/", "BaseAlert/test").Radio(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/radio" {
		t.Errorf("path = %q, want /v1/radio", gotPath)
	}
	if gotUA != "BaseAlert/test" {
		t.Errorf("User-Agent = %q", gotUA)
	}
	if err := snap.Check(StationNames()); err != nil {
		t.Fatalf("Check: %v", err)
	}

	tb := snap.Stations["TechnoBase"]
	want := Station{
		Name:  "TechnoBase",
		Live:  true,
		DJ:    "BlueCore",
		DJID:  525081,
		Show:  "Happy Hardcore Bass Kick",
		Style: "Happy Hardcore",
		Start: time.UnixMilli(1791568800000),
		End:   time.UnixMilli(1791576000000),
	}
	if tb != want {
		t.Errorf("TechnoBase = %+v\nwant         %+v", tb, want)
	}
	// 1791568800000 ms is Friday 2026-10-09 20:00 in Berlin.
	if got := tb.Start.UTC().Format(time.RFC3339); got != "2026-10-09T18:00:00Z" {
		t.Errorf("start = %s", got)
	}

	trb := snap.Stations["TranceBase"]
	if trb.Live || trb.DJ != "" || !trb.Start.IsZero() {
		t.Errorf("TranceBase should be a plain playlist entry, got %+v", trb)
	}
}

func TestRadioTolerantTypes(t *testing.T) {
	c := serve(t, 200, `{
		"TechnoBase": {"dj": "  BlueCore ", "id": "525081", "st": "1791568800000", "en": 1.791576e12},
		"HouseTime":  {"dj": "Ghost", "p": "true"},
		"HardBase":   {"p": 1},
		"TranceBase": {"dj": "NoTimes", "id": null}
	}`)
	snap, err := c.Radio(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := snap.Check(StationNames()); err != nil {
		t.Fatalf("Check: %v", err)
	}
	tb := snap.Stations["TechnoBase"]
	if tb.DJ != "BlueCore" || tb.DJID != 525081 {
		t.Errorf("TechnoBase DJ = %q (%d)", tb.DJ, tb.DJID)
	}
	if !tb.Start.Equal(time.UnixMilli(1791568800000)) || !tb.End.Equal(time.UnixMilli(1791576000000)) {
		t.Errorf("TechnoBase times = %v – %v", tb.Start, tb.End)
	}
	if snap.Stations["HouseTime"].Live {
		t.Error("the playlist flag must win over a DJ name")
	}
	if snap.Stations["HardBase"].Live {
		t.Error("p: 1 must count as playlist")
	}
	nt := snap.Stations["TranceBase"]
	if !nt.Live || nt.DJID != 0 || !nt.Start.IsZero() || !nt.End.IsZero() {
		t.Errorf("TranceBase = %+v", nt)
	}
}

func TestRadioErrors(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantKind   string
		wantStatus int
	}{
		{"server error", 500, `{"error":"boom"}`, KindHTTPStatus, 500},
		{"not found", 404, `{"status":404}`, KindHTTPStatus, 404},
		{"html instead of json", 200, `<html>maintenance</html>`, KindInvalid, 200},
		{"array instead of object", 200, `[]`, KindInvalid, 200},
		{"no stations", 200, `{}`, KindInvalid, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := serve(t, tt.status, tt.body).Radio(context.Background())
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := ErrorKind(err); got != tt.wantKind {
				t.Errorf("kind = %s, want %s (%v)", got, tt.wantKind, err)
			}
			if got := ErrorStatus(err); got != tt.wantStatus {
				t.Errorf("status = %d, want %d", got, tt.wantStatus)
			}
		})
	}
}

func TestCheckDetectsFormatChanges(t *testing.T) {
	// A renamed "dj" field would otherwise look like four silent playlists.
	c := serve(t, 200, `{"TechnoBase": {"moderator": "BlueCore", "id": 1}, "HouseTime": {"p": true}}`)
	snap, err := c.Radio(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := snap.Check([]string{"HouseTime"}); err != nil {
		t.Errorf("HouseTime alone is fine, got %v", err)
	}
	for _, name := range []string{"TechnoBase", "HardBase"} {
		err := snap.Check([]string{"HouseTime", name})
		if err == nil || ErrorKind(err) != KindInvalid {
			t.Errorf("Check(%s) = %v, want %s", name, err, KindInvalid)
		}
	}
}

func TestRadioTimeoutAndNetwork(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	c := New(srv.URL, "")
	c.http.Timeout = 50 * time.Millisecond
	_, err := c.Radio(context.Background())
	if got := ErrorKind(err); got != KindTimeout {
		t.Errorf("slow server: kind = %s, want %s (%v)", got, KindTimeout, err)
	}
	close(release)
	srv.Close()

	// The server is gone now, so the connection is refused.
	_, err = c.Radio(context.Background())
	if got := ErrorKind(err); got != KindNetwork {
		t.Errorf("closed server: kind = %s, want %s (%v)", got, KindNetwork, err)
	}
}

func TestShowplanFixture(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(fixture(t, "showplan.json")))
	}))
	defer srv.Close()

	shows, err := New(srv.URL, "").Showplan(context.Background(), 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/showplan/5/1" {
		t.Errorf("path = %q", gotPath)
	}
	if len(shows) != 7 {
		t.Fatalf("got %d shows, want 7", len(shows))
	}
	want := Show{
		Name:  "Happy Hardcore Bass Kick",
		DJ:    "BlueCore",
		DJID:  525081,
		Style: "Happy Hardcore",
		Start: time.UnixMilli(1791568800000),
		End:   time.UnixMilli(1791576000000),
	}
	if shows[5] != want {
		t.Errorf("shows[5] = %+v\nwant       %+v", shows[5], want)
	}
}

func TestCanonicalStation(t *testing.T) {
	for in, want := range map[string]string{
		"TechnoBase":    "TechnoBase",
		"technobase":    "TechnoBase",
		" HardBase.FM ": "HardBase",
		"TRANCEBASE.fm": "TranceBase",
		"housetime":     "HouseTime",
	} {
		if got, ok := CanonicalStation(in); !ok || got != want {
			t.Errorf("CanonicalStation(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "CoreTime", "Techno Base"} {
		if got, ok := CanonicalStation(in); ok {
			t.Errorf("CanonicalStation(%q) = %q, want no match", in, got)
		}
	}
}
