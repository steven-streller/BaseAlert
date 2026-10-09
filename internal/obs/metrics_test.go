package obs

import (
	"bytes"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

func scrape(m *Metrics) string {
	var buf bytes.Buffer
	m.WritePrometheus(&buf)
	return buf.String()
}

// populated returns metrics in which every family has at least one series.
func populated() *Metrics {
	m := NewMetrics("1.2.3")
	m.WatchStations([]string{"TechnoBase", "HardBase"})
	m.ObservePoll(ResultOK, 120*time.Millisecond)
	m.ConfigLoaded(3, 1)
	m.PublishStations([]StationGauge{
		{Station: "TechnoBase", Live: true, FavoriteLive: true, SlotActive: false},
		{Station: "HardBase", Live: false, FavoriteLive: false, SlotActive: true},
	}, time.Minute)
	return m
}

// Metric names and types are an interface: dashboards and alerts break when
// they change. Changing them means changing the golden file on purpose.
func TestMetricFamiliesMatchGoldenFile(t *testing.T) {
	golden, err := os.ReadFile("testdata/metric_families.golden")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, match := range regexp.MustCompile(`(?m)^# TYPE (basealert_\S+) (\S+)$`).FindAllStringSubmatch(scrape(populated()), -1) {
		got = append(got, match[1]+" "+match[2])
	}
	sort.Strings(got)
	if strings.Join(got, "\n")+"\n" != string(golden) {
		t.Errorf("exported families:\n%s\n\nwant (testdata/metric_families.golden):\n%s", strings.Join(got, "\n"), golden)
	}

	var catalog []string
	for _, info := range Catalog {
		catalog = append(catalog, info.Name+" "+info.Type)
	}
	sort.Strings(catalog)
	if strings.Join(catalog, "\n")+"\n" != string(golden) {
		t.Errorf("Catalog does not match the golden file:\n%s", strings.Join(catalog, "\n"))
	}
}

func TestEveryFamilyHasHelpAndTypeExactlyOnce(t *testing.T) {
	out := scrape(populated())
	for _, info := range Catalog {
		help := "# HELP " + info.Name + " " + info.Help + "\n"
		typ := "# TYPE " + info.Name + " " + info.Type + "\n"
		if n := strings.Count(out, help); n != 1 {
			t.Errorf("%s: %d HELP lines with text, want 1", info.Name, n)
		}
		if n := strings.Count(out, typ); n != 1 {
			t.Errorf("%s: %d TYPE lines, want 1", info.Name, n)
		}
		if strings.Contains(out, "# HELP "+info.Name+"\n") {
			t.Errorf("%s: HELP line without text survived", info.Name)
		}
	}
}

func TestOutputIsWellFormed(t *testing.T) {
	sample := regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*(\{[a-zA-Z_][a-zA-Z0-9_]*="(\\.|[^"\\])*"(, ?[a-zA-Z_][a-zA-Z0-9_]*="(\\.|[^"\\])*")*\})? (NaN|[+-]?Inf|[-+]?[0-9.]+(e[-+]?[0-9]+)?)$`)
	out := scrape(populated())
	if !strings.HasSuffix(out, "\n") {
		t.Error("output must end with a newline")
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if strings.HasPrefix(line, "# HELP ") || strings.HasPrefix(line, "# TYPE ") {
			continue
		}
		if !sample.MatchString(line) {
			t.Errorf("malformed line: %q", line)
		}
		series := line[:strings.LastIndex(line, " ")]
		if seen[series] {
			t.Errorf("series exported twice: %s", series)
		}
		seen[series] = true
	}
	for _, runtimeMetric := range []string{"go_goroutines ", "go_memstats_heap_inuse_bytes ", "process_resident_memory_bytes ", "process_cpu_seconds_total ", "process_start_time_seconds "} {
		if !strings.Contains(out, "\n"+runtimeMetric) {
			t.Errorf("runtime metric %smissing", runtimeMetric)
		}
	}
}

func TestCountersStartAtZeroAndCount(t *testing.T) {
	m := NewMetrics("dev")
	m.WatchStations([]string{"TechnoBase"})
	out := scrape(m)
	for _, series := range []string{
		`basealert_polls_total{result="ok"} 0`,
		`basealert_polls_total{result="timeout"} 0`,
		`basealert_polls_total{result="network"} 0`,
		`basealert_polls_total{result="http_status"} 0`,
		`basealert_polls_total{result="invalid_response"} 0`,
		`basealert_notifications_total{kind="favorite",result="sent",station="TechnoBase"} 0`,
		`basealert_notifications_total{kind="slot",result="failed",station="TechnoBase"} 0`,
		`basealert_notifications_total{kind="slot",result="rejected",station="TechnoBase"} 0`,
		`basealert_notifications_total{kind="outage",result="sent"} 0`,
		`basealert_notifications_total{kind="recovery",result="sent"} 0`,
		`basealert_notifications_total{kind="config_error",result="rejected"} 0`,
		`basealert_poll_duration_seconds_count 0`,
	} {
		if !strings.Contains(out, series+"\n") {
			t.Errorf("missing at start: %s", series)
		}
	}

	m.ObservePoll(ResultOK, 80*time.Millisecond)
	m.ObservePoll("timeout", 10*time.Second)
	m.ObservePoll("timeout", 10*time.Second)
	m.CountNotification(KindFavorite, ResultSent, "TechnoBase")
	m.CountNotification(KindOutage, ResultFailed, "")
	out = scrape(m)
	for _, series := range []string{
		`basealert_polls_total{result="ok"} 1`,
		`basealert_polls_total{result="timeout"} 2`,
		`basealert_notifications_total{kind="favorite",result="sent",station="TechnoBase"} 1`,
		`basealert_notifications_total{kind="outage",result="failed"} 1`,
		`basealert_poll_duration_seconds_bucket{le="0.05"} 0`,
		`basealert_poll_duration_seconds_bucket{le="0.1"} 1`,
		`basealert_poll_duration_seconds_bucket{le="5"} 1`,
		`basealert_poll_duration_seconds_bucket{le="10"} 3`,
		`basealert_poll_duration_seconds_bucket{le="+Inf"} 3`,
		`basealert_poll_duration_seconds_count 3`,
	} {
		if !strings.Contains(out, series+"\n") {
			t.Errorf("missing after counting: %s", series)
		}
	}
}

func TestPollTimestampOnlyMovesOnSuccess(t *testing.T) {
	m := NewMetrics("dev")
	clock := time.Unix(1_791_568_800, 500_000_000)
	m.now = func() time.Time { return clock }

	m.ObservePoll("network", time.Second)
	if !strings.Contains(scrape(m), MetricPollLastSuccess+" 0\n") {
		t.Error("a failed poll must not set the success timestamp")
	}
	m.ObservePoll(ResultOK, time.Second)
	if !strings.Contains(scrape(m), MetricPollLastSuccess+" 1.7915688005e+09\n") {
		t.Errorf("success timestamp not set:\n%s", grep(scrape(m), MetricPollLastSuccess))
	}
	clock = clock.Add(time.Hour)
	m.ObservePoll("timeout", time.Second)
	if !strings.Contains(scrape(m), MetricPollLastSuccess+" 1.7915688005e+09\n") {
		t.Error("a later failure must leave the success timestamp alone")
	}
}

func TestConfigMetrics(t *testing.T) {
	m := NewMetrics("dev")
	clock := time.Unix(1_791_568_800, 0)
	m.now = func() time.Time { return clock }

	if !strings.Contains(scrape(m), MetricConfigReloadOK+" 0\n") {
		t.Error("without a loaded config the reload gauge must be 0")
	}
	m.ConfigLoaded(12, 2)
	out := scrape(m)
	for _, series := range []string{
		MetricConfigReloadOK + " 1",
		MetricConfigReloadTime + " 1791568800",
		MetricConfigFavorites + " 12",
		MetricConfigSlots + " 2",
	} {
		if !strings.Contains(out, series+"\n") {
			t.Errorf("missing: %s\n%s", series, grep(out, "basealert_config"))
		}
	}

	// A rejected file flips the flag but keeps describing the active config.
	clock = clock.Add(time.Hour)
	m.ConfigRejected()
	out = scrape(m)
	for _, series := range []string{
		MetricConfigReloadOK + " 0",
		MetricConfigReloadTime + " 1791568800",
		MetricConfigFavorites + " 12",
	} {
		if !strings.Contains(out, series+"\n") {
			t.Errorf("after rejection, missing: %s", series)
		}
	}
}

func TestStationGaugesExpire(t *testing.T) {
	m := NewMetrics("dev")
	clock := time.Unix(1_791_568_800, 0)
	m.now = func() time.Time { return clock }

	if strings.Contains(scrape(m), "basealert_station_") {
		t.Error("no station gauges before the first poll")
	}

	m.PublishStations([]StationGauge{
		{Station: "TechnoBase", Live: true, FavoriteLive: true},
		{Station: "HardBase", SlotActive: true},
	}, 3*time.Minute)
	out := scrape(m)
	for _, series := range []string{
		`basealert_station_live{station="TechnoBase"} 1`,
		`basealert_station_live{station="HardBase"} 0`,
		`basealert_station_favorite_live{station="TechnoBase"} 1`,
		`basealert_station_favorite_live{station="HardBase"} 0`,
		`basealert_station_slot_active{station="TechnoBase"} 0`,
		`basealert_station_slot_active{station="HardBase"} 1`,
	} {
		if !strings.Contains(out, series+"\n") {
			t.Errorf("missing: %s", series)
		}
	}

	clock = clock.Add(3 * time.Minute)
	if !strings.Contains(scrape(m), "basealert_station_live") {
		t.Error("gauges must still be exported at the end of their validity")
	}
	// Polling has stopped: the series end instead of freezing at "live".
	clock = clock.Add(time.Second)
	if out := scrape(m); strings.Contains(out, "basealert_station_") {
		t.Errorf("stale station gauges are still exported:\n%s", grep(out, "basealert_station_"))
	}
	// The other metrics are unaffected.
	if !strings.Contains(scrape(m), MetricPolls) {
		t.Error("counters must not disappear")
	}
}

func TestBuildInfoAndLabelQuoting(t *testing.T) {
	out := scrape(NewMetrics(`v1 "rc"\x`))
	want := `basealert_build_info{version="v1 \"rc\"\\x",goversion="`
	if !strings.Contains(out, want) {
		t.Errorf("build info not quoted as expected:\n%s", grep(out, MetricBuildInfo))
	}
	if got := quote("a\nb"); got != `"a\nb"` {
		t.Errorf("quote newline = %s", got)
	}
}

func grep(text, needle string) string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, needle) {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
