// Package metrics exposes the archive as Prometheus metrics: the open
// findings of the latest snapshot per system, its age, what the catcher
// archived, the consumer lag and the latest cluster analysis. The archived
// JSON stays the source of truth; the gauges are a view of the newest
// snapshot per system, rebuilt from the archive after a restart.
package metrics

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/stuttgart-things/homerun2-s3-catcher/internal/lag"
)

const (
	namespace   = "homerun"
	labelSystem = "system"
	labelStream = "stream"
	labelSource = "source"
	labelTarget = "target"
)

// LagReporter returns the latest lag measurement.
type LagReporter interface {
	Report() lag.Report
}

// Metrics holds the collectors on their own registry. Safe for concurrent
// use.
type Metrics struct {
	reg *prometheus.Registry
	now func() time.Time

	findings   *prometheus.GaugeVec
	snapshotAt *prometheus.GaugeVec
	truncated  *prometheus.GaugeVec
	open       *prometheus.GaugeVec
	archived   *prometheus.CounterVec
	errors     *prometheus.CounterVec
	unarchived *prometheus.CounterVec
	analysisOpen, analysisNew, analysisResolved,
	analysisAt prometheus.Gauge
	analysisSeverity *prometheus.GaugeVec

	mu     sync.Mutex
	latest map[string]time.Time // system -> time of the snapshot in the gauges
}

// New registers the collectors; lagr may be nil.
func New(lagr LagReporter) *Metrics {
	m := &Metrics{
		reg:    prometheus.NewRegistry(),
		now:    time.Now,
		latest: map[string]time.Time{},
		findings: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace, Name: "findings",
			Help: "Open findings in the latest snapshot of a system, per kind and severity.",
		}, []string{labelSystem, labelSource, labelTarget, "kind", "severity"}),
		snapshotAt: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace, Name: "snapshot_timestamp_seconds",
			Help: "Time of the latest snapshot of a system (Unix seconds).",
		}, []string{labelSystem, labelSource, labelTarget}),
		truncated: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace, Name: "snapshot_truncated_findings",
			Help: "Findings the source left out of the latest snapshot to stay below the payload limit.",
		}, []string{labelSystem, labelSource, labelTarget}),
		open: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace, Name: "snapshot_open_findings",
			Help: "Open findings in the latest snapshot of a system in total, 0 when clean.",
		}, []string{labelSystem, labelSource, labelTarget}),
		archived: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "archived_messages_total",
			Help: "Messages written to S3.",
		}, []string{labelStream, labelSystem}),
		errors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "archive_errors_total",
			Help: "Failed archive attempts (JSON.GET or upload); the entry is delivered again.",
		}, []string{labelStream}),
		unarchived: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "unarchived_messages_total",
			Help: "Entries acknowledged without an upload, by reason (skipped, missing, invalid).",
		}, []string{labelStream, "reason"}),
		analysisOpen: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace, Name: "analysis_open_findings",
			Help: "Open findings in the latest cluster analysis.",
		}),
		analysisNew: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace, Name: "analysis_new_findings",
			Help: "Findings new since the previous cluster analysis.",
		}),
		analysisResolved: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace, Name: "analysis_resolved_findings",
			Help: "Findings resolved since the previous cluster analysis.",
		}),
		analysisAt: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace, Name: "analysis_timestamp_seconds",
			Help: "Generation time of the latest cluster analysis (Unix seconds).",
		}),
		analysisSeverity: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace, Name: "analysis_severity",
			Help: "Severity of the latest cluster analysis: 1 for the current one.",
		}, []string{"severity"}),
	}
	m.reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.findings, m.snapshotAt, m.truncated, m.open, m.archived, m.errors, m.unarchived,
		m.analysisOpen, m.analysisNew, m.analysisResolved, m.analysisAt, m.analysisSeverity,
	)
	if lagr != nil {
		m.reg.MustRegister(&lagCollector{lagr: lagr})
	}
	return m
}

// Handler serves the metrics.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{Registry: m.reg})
}

// Archived counts an uploaded message and, if it is a findings snapshot,
// makes it the system's latest. doc is the message (the Redis JSON
// document).
func (m *Metrics) Archived(stream, system string, doc []byte) {
	m.archived.WithLabelValues(stream, system).Inc()
	if s, ok := ParseMessage(doc, m.now()); ok {
		m.Apply(s)
	}
}

// Failed counts a failed archive attempt.
func (m *Metrics) Failed(stream string) { m.errors.WithLabelValues(stream).Inc() }

// Unarchived counts an entry acknowledged without an upload.
func (m *Metrics) Unarchived(stream, reason string) {
	m.unarchived.WithLabelValues(stream, reason).Inc()
}

// Apply replaces the system's series with the snapshot: kinds that are gone
// disappear. A snapshot older than the one in the gauges (a redelivery, the
// rebuild racing a live message) is ignored; it reports whether s was
// applied.
func (m *Metrics) Apply(s Snapshot) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if prev, ok := m.latest[s.System]; ok && s.At.Before(prev) {
		return false
	}
	m.latest[s.System] = s.At

	match := prometheus.Labels{labelSystem: s.System}
	m.findings.DeletePartialMatch(match)
	m.snapshotAt.DeletePartialMatch(match)
	m.truncated.DeletePartialMatch(match)
	m.open.DeletePartialMatch(match)

	for k, n := range s.Counts {
		m.findings.WithLabelValues(s.System, s.Source, s.Target, k.Kind, k.Severity).Set(float64(n))
	}
	m.snapshotAt.WithLabelValues(s.System, s.Source, s.Target).Set(float64(s.At.Unix()))
	m.truncated.WithLabelValues(s.System, s.Source, s.Target).Set(float64(s.Truncated))
	m.open.WithLabelValues(s.System, s.Source, s.Target).Set(float64(s.Open()))
	slog.Debug("snapshot gauges updated", "system", s.System, "open", s.Open(), "at", s.At)
	return true
}

// lagCollector reads the lag monitor's last measurement on every scrape.
type lagCollector struct{ lagr LagReporter }

var (
	lagDesc = prometheus.NewDesc(namespace+"_consumer_lag_seconds",
		"Age of the oldest unarchived entry of a stream in seconds (pending or not yet delivered).",
		[]string{labelStream}, nil)
	pendingDesc = prometheus.NewDesc(namespace+"_consumer_pending",
		"Entries delivered to the consumer group but not acknowledged.",
		[]string{labelStream}, nil)
)

func (c *lagCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- lagDesc
	ch <- pendingDesc
}

func (c *lagCollector) Collect(ch chan<- prometheus.Metric) {
	for stream, s := range c.lagr.Report().Streams {
		ch <- prometheus.MustNewConstMetric(lagDesc, prometheus.GaugeValue, s.Lag().Seconds(), stream)
		ch <- prometheus.MustNewConstMetric(pendingDesc, prometheus.GaugeValue, float64(s.Pending), stream)
	}
}
