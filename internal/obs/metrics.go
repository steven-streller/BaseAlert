package obs

import (
	"bytes"
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/VictoriaMetrics/metrics"
)

// Metric names.
const (
	MetricStationLive         = "basealert_station_live"
	MetricStationFavoriteLive = "basealert_station_favorite_live"
	MetricStationSlotActive   = "basealert_station_slot_active"
	MetricNotifications       = "basealert_notifications_total"
	MetricPolls               = "basealert_polls_total"
	MetricPollDuration        = "basealert_poll_duration_seconds"
	MetricPollLastSuccess     = "basealert_poll_last_success_timestamp_seconds"
	MetricConfigReloadOK      = "basealert_config_last_reload_successful"
	MetricConfigReloadTime    = "basealert_config_last_reload_success_timestamp_seconds"
	MetricConfigFavorites     = "basealert_config_favorites"
	MetricConfigSlots         = "basealert_config_slots"
	MetricBuildInfo           = "basealert_build_info"
)

// MetricInfo describes one metric family.
type MetricInfo struct {
	Name string
	Type string
	Help string
}

// Catalog lists every metric family the application exports itself. The Go
// runtime and process metrics of the library come on top.
var Catalog = []MetricInfo{
	{MetricStationLive, "gauge", "1 if a DJ is live on the station, 0 if the playlist is running."},
	{MetricStationFavoriteLive, "gauge", "1 if a favourite DJ is live on the station."},
	{MetricStationSlotActive, "gauge", "1 if a configured time slot is open for the station."},
	{MetricNotifications, "counter", "Push notification attempts by kind and result (sent, failed = will be retried, rejected = given up)."},
	{MetricPolls, "counter", "Polls of the station API by result."},
	{MetricPollDuration, "histogram", "Duration of a poll of the station API in seconds."},
	{MetricPollLastSuccess, "gauge", "Unix time of the last successful poll of the station API."},
	{MetricConfigReloadOK, "gauge", "1 if the current content of the config file is valid, 0 if it was rejected."},
	{MetricConfigReloadTime, "gauge", "Unix time of the last successful load of the config file."},
	{MetricConfigFavorites, "gauge", "Number of favourite DJs in the active config."},
	{MetricConfigSlots, "gauge", "Number of time slots in the active config."},
	{MetricBuildInfo, "gauge", "Build information, constant 1."},
}

// Label values of basealert_notifications_total.
const (
	KindFavorite    = "favorite"
	KindSlot        = "slot"
	KindOutage      = "outage"
	KindRecovery    = "recovery"
	KindConfigError = "config_error"

	ResultSent     = "sent"
	ResultFailed   = "failed"
	ResultRejected = "rejected"
)

// ResultOK is the result label of a successful poll. Failed polls use the
// error kinds of package wao.
const ResultOK = "ok"

var (
	stationKinds        = []string{KindFavorite, KindSlot}
	systemKinds         = []string{KindOutage, KindRecovery, KindConfigError}
	notificationResults = []string{ResultSent, ResultFailed, ResultRejected}
	pollResults         = []string{ResultOK, "timeout", "network", "http_status", "invalid_response"}
	pollBuckets         = []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
)

// StationGauge is the view of one station that is exported as gauges.
type StationGauge struct {
	Station      string
	Live         bool
	FavoriteLive bool
	SlotActive   bool
}

type stationView struct {
	gauges     []StationGauge
	validUntil time.Time
}

// Metrics is the set of metrics of one application instance.
type Metrics struct {
	set  *metrics.Set
	help map[string]string
	now  func() time.Time

	pollDuration    *metrics.PrometheusHistogram
	pollLastSuccess *metrics.Gauge
	configOK        *metrics.Gauge
	configLoadedAt  *metrics.Gauge
	configFavorites *metrics.Gauge
	configSlots     *metrics.Gauge

	stations atomic.Pointer[stationView]
}

// NewMetrics creates the metrics. Counters that do not depend on a station
// start at 0 right away, so that rate() and increase() see every event.
func NewMetrics(version string) *Metrics {
	// The library only writes "# TYPE" lines when asked to. They are needed
	// for counters and histograms to be recognised as such.
	metrics.ExposeMetadata(true)

	m := &Metrics{set: metrics.NewSet(), help: map[string]string{}, now: time.Now}
	for _, info := range Catalog {
		m.help[info.Name] = info.Help
	}

	m.pollDuration = m.set.NewPrometheusHistogramExt(MetricPollDuration, pollBuckets)
	m.pollLastSuccess = m.set.NewGauge(MetricPollLastSuccess, nil)
	m.configOK = m.set.NewGauge(MetricConfigReloadOK, nil)
	m.configLoadedAt = m.set.NewGauge(MetricConfigReloadTime, nil)
	m.configFavorites = m.set.NewGauge(MetricConfigFavorites, nil)
	m.configSlots = m.set.NewGauge(MetricConfigSlots, nil)
	m.set.NewGauge(fmt.Sprintf(`%s{version=%s,goversion=%s}`, MetricBuildInfo, quote(version), quote(runtime.Version())), nil).Set(1)

	for _, result := range pollResults {
		m.set.GetOrCreateCounter(pollCounter(result))
	}
	for _, kind := range systemKinds {
		for _, result := range notificationResults {
			m.set.GetOrCreateCounter(notificationCounter(kind, result, ""))
		}
	}
	m.set.RegisterMetricsWriter(m.writeStations)
	return m
}

// WatchStations creates the per-station notification counters at 0.
func (m *Metrics) WatchStations(stations []string) {
	for _, station := range stations {
		for _, kind := range stationKinds {
			for _, result := range notificationResults {
				m.set.GetOrCreateCounter(notificationCounter(kind, result, station))
			}
		}
	}
}

// ObservePoll records one poll of the station API.
func (m *Metrics) ObservePoll(result string, duration time.Duration) {
	m.set.GetOrCreateCounter(pollCounter(result)).Inc()
	m.pollDuration.Update(duration.Seconds())
	if result == ResultOK {
		m.pollLastSuccess.Set(unixSeconds(m.now()))
	}
}

// CountNotification records one attempt to send a push. station is empty for
// notifications that are not about a station.
func (m *Metrics) CountNotification(kind, result, station string) {
	m.set.GetOrCreateCounter(notificationCounter(kind, result, station)).Inc()
}

// ConfigLoaded records that the config file was read and found valid.
func (m *Metrics) ConfigLoaded(favorites, slots int) {
	m.configOK.Set(1)
	m.configLoadedAt.Set(unixSeconds(m.now()))
	m.configFavorites.Set(float64(favorites))
	m.configSlots.Set(float64(slots))
}

// ConfigRejected records that the current content of the config file is
// invalid. The counts keep describing the config that is still active.
func (m *Metrics) ConfigRejected() {
	m.configOK.Set(0)
}

// PublishStations replaces the station gauges. They are exported for the
// given duration only: if polling stops, the series end instead of freezing
// at their last value.
func (m *Metrics) PublishStations(gauges []StationGauge, validFor time.Duration) {
	m.stations.Store(&stationView{gauges: gauges, validUntil: m.now().Add(validFor)})
}

// WritePrometheus writes all metrics in the Prometheus text format.
func (m *Metrics) WritePrometheus(w io.Writer) {
	var buf bytes.Buffer
	m.set.WritePrometheus(&buf)
	// The library writes "# HELP name" without a text. Fill in ours.
	for line := range bytes.Lines(buf.Bytes()) {
		if name, ok := bytes.CutPrefix(bytes.TrimRight(line, "\n"), []byte("# HELP ")); ok {
			if help := m.help[string(name)]; help != "" {
				fmt.Fprintf(w, "# HELP %s %s\n", name, help)
				continue
			}
		}
		_, _ = w.Write(line)
	}
	metrics.WriteProcessMetrics(w)
}

func (m *Metrics) writeStations(w io.Writer) {
	view := m.stations.Load()
	if view == nil || len(view.gauges) == 0 || m.now().After(view.validUntil) {
		return
	}
	families := []struct {
		name  string
		value func(StationGauge) bool
	}{
		{MetricStationFavoriteLive, func(g StationGauge) bool { return g.FavoriteLive }},
		{MetricStationLive, func(g StationGauge) bool { return g.Live }},
		{MetricStationSlotActive, func(g StationGauge) bool { return g.SlotActive }},
	}
	for _, family := range families {
		metrics.WriteMetadataIfNeeded(w, family.name, "gauge")
		for _, g := range view.gauges {
			value := 0
			if family.value(g) {
				value = 1
			}
			fmt.Fprintf(w, "%s{station=%s} %d\n", family.name, quote(g.Station), value)
		}
	}
}

func pollCounter(result string) string {
	return fmt.Sprintf(`%s{result=%s}`, MetricPolls, quote(result))
}

func notificationCounter(kind, result, station string) string {
	if station == "" {
		return fmt.Sprintf(`%s{kind=%s,result=%s}`, MetricNotifications, quote(kind), quote(result))
	}
	return fmt.Sprintf(`%s{kind=%s,result=%s,station=%s}`, MetricNotifications, quote(kind), quote(result), quote(station))
}

var labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

// quote formats a label value as the exposition format requires.
func quote(v string) string {
	return `"` + labelEscaper.Replace(v) + `"`
}

func unixSeconds(t time.Time) float64 {
	return float64(t.UnixMilli()) / 1000
}
