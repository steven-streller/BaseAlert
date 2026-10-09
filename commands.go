package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"basealert/internal/config"
	"basealert/internal/pushover"
	"basealert/internal/wao"
)

var weekdays = [...]string{"So", "Mo", "Di", "Mi", "Do", "Fr", "Sa"}

// showplanPause is the wait between two schedule requests of the djs
// command. The API is not ours, so there is no reason to hurry.
var showplanPause = 100 * time.Millisecond

// commandConfig loads the config for a one-off command. A missing file is
// not an error there: the command then works with the defaults.
func commandConfig(set settings) (*config.Config, error) {
	data, err := os.ReadFile(set.ConfigPath)
	if errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(set.Stderr, "Hinweis: %s nicht gefunden, es gelten die Standardwerte (alle Sender, keine Favoriten).\n\n", set.ConfigPath)
		return config.Parse(nil)
	}
	if err != nil {
		return nil, err
	}
	cfg, err := config.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", set.ConfigPath, err)
	}
	return cfg, nil
}

func fail(set settings, err error) int {
	fmt.Fprintln(set.Stderr, "basealert:", err)
	return 1
}

// cmdNow prints the live status of the watched stations and which rule
// would match right now.
func cmdNow(set settings) int {
	cfg, err := commandConfig(set)
	if err != nil {
		return fail(set, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	snap, err := wao.New(set.APIURL, userAgent()).Radio(ctx)
	if err == nil {
		err = snap.Check(cfg.Stations)
	}
	if err != nil {
		return fail(set, fmt.Errorf("Sender-API: %w", err))
	}

	now := time.Now()
	fmt.Fprintf(set.Stdout, "Stand %s Uhr (%s), %d Favoriten, %d Zeitslots\n\n",
		now.In(cfg.Location).Format("02.01.2006 15:04"), cfg.Location, len(cfg.Favorites), len(cfg.Slots))

	w := tabwriter.NewWriter(set.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SENDER\tSTATUS\tDJ\tDJ-ID\tSHOW\tZEIT\tFAVORIT\tZEITSLOT")
	for _, name := range cfg.Stations {
		st := snap.Stations[name]
		slot := "–"
		if cfg.SlotActive(name, now) {
			slot = "aktiv"
		}
		if !st.Live {
			fmt.Fprintf(w, "%s\tPlaylist\t–\t–\t–\t–\t–\t%s\n", name, slot)
			continue
		}
		favorite := "–"
		if cfg.IsFavorite(st.DJ, st.DJID) {
			favorite = "ja"
		}
		fmt.Fprintf(w, "%s\tlive\t%s\t%d\t%s\t%s\t%s\t%s\n", name, st.DJ, st.DJID,
			orDash(st.Show), orDash(strings.TrimSuffix(clockRange(st.Start, st.End, cfg.Location), " Uhr")), favorite, slot)
	}
	_ = w.Flush()
	return 0
}

// cmdDJs lists the DJs of the published schedule with their IDs, as a help
// for filling the favourites list.
func cmdDJs(set settings) int {
	cfg, err := commandConfig(set)
	if err != nil {
		return fail(set, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	api := wao.New(set.APIURL, userAgent())

	type entry struct {
		name     string
		id       int64
		stations map[string]bool
		next     time.Time
		nextOn   string
	}
	djs := map[string]*entry{}
	now := time.Now()

	for _, station := range wao.Stations {
		if !cfg.Watches(station.Name) {
			continue
		}
		for day := range wao.ShowplanDays {
			shows, err := api.Showplan(ctx, station.ID, day)
			if err != nil {
				return fail(set, fmt.Errorf("Sendeplan %s, Tag %d: %w", station.Name, day, err))
			}
			for _, show := range shows {
				if show.DJ == "" {
					continue
				}
				key := strconv.FormatInt(show.DJID, 10) + "/" + strings.ToLower(show.DJ)
				e := djs[key]
				if e == nil {
					e = &entry{name: show.DJ, id: show.DJID, stations: map[string]bool{}}
					djs[key] = e
				}
				e.stations[station.Name] = true
				if show.End.After(now) && (e.next.IsZero() || show.Start.Before(e.next)) {
					e.next, e.nextOn = show.Start, station.Name
				}
			}
			time.Sleep(showplanPause)
		}
	}

	list := make([]*entry, 0, len(djs))
	for _, e := range djs {
		list = append(list, e)
	}
	sort.Slice(list, func(i, j int) bool { return strings.ToLower(list[i].name) < strings.ToLower(list[j].name) })

	w := tabwriter.NewWriter(set.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "DJ\tDJ-ID\tSENDER\tNÄCHSTE SENDUNG\tFAVORIT")
	for _, e := range list {
		var stations []string
		for _, name := range wao.StationNames() {
			if e.stations[name] {
				stations = append(stations, name)
			}
		}
		next := "–"
		if !e.next.IsZero() {
			local := e.next.In(cfg.Location)
			next = fmt.Sprintf("%s %s %s", weekdays[local.Weekday()], local.Format("02.01. 15:04"), e.nextOn)
		}
		favorite := "–"
		if cfg.IsFavorite(e.name, e.id) {
			favorite = "ja"
		}
		fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\n", e.name, e.id, strings.Join(stations, ", "), next, favorite)
	}
	_ = w.Flush()
	fmt.Fprintf(set.Stdout, "\n%d DJs im Sendeplan von gestern bis in sieben Tagen.\n", len(list))
	return 0
}

// cmdTest sends a test push, which verifies the Pushover credentials.
func cmdTest(set settings) int {
	if set.Token == "" || set.User == "" {
		return fail(set, errors.New("PUSHOVER_TOKEN und PUSHOVER_USER müssen gesetzt sein"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	receipt, err := pushover.New(set.Token, set.User, set.Device, set.PushoverURL, userAgent()).Send(ctx, pushover.Message{
		Title:    "BaseAlert Test",
		Body:     "Die Pushover-Zugangsdaten funktionieren.\nSo sehen Benachrichtigungen von BaseAlert aus.",
		URL:      playerURL + "TechnoBase",
		URLTitle: "Reinhören",
	})
	if err != nil {
		return fail(set, err)
	}
	fmt.Fprintf(set.Stdout, "Test-Push gesendet (Pushover-Request %s).\n", receipt.Request)
	return 0
}

// cmdHealth asks the running daemon for its health. It exists because the
// image has neither a shell nor curl for a container health check.
func cmdHealth(set settings) int {
	if set.HTTPAddr == "" {
		return fail(set, errors.New("BASEALERT_HTTP_ADDR ist leer, der HTTP-Server ist abgeschaltet"))
	}
	host, port, err := net.SplitHostPort(set.HTTPAddr)
	if err != nil {
		return fail(set, fmt.Errorf("BASEALERT_HTTP_ADDR: %w", err))
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		return fail(set, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fail(set, fmt.Errorf("/healthz antwortet mit %s", resp.Status))
	}
	fmt.Fprintln(set.Stdout, "ok")
	return 0
}

func orDash(s string) string {
	if s == "" {
		return "–"
	}
	return s
}
