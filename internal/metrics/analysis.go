package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/stuttgart-things/homerun2-s3-catcher/internal/archive"
)

// Getter reads one object.
type Getter interface {
	Get(ctx context.Context, key string, maxBytes int64) ([]byte, error)
}

// maxReport bounds what is read of an analysis report.
const maxReport = 4 << 20

// Report is the part of a cluster-analysis report (homerun2-ai-pitcher,
// analysis/date=<day>/<runId>.json) the metrics need.
type Report struct {
	GeneratedAt time.Time `json:"generatedAt"`
	Severity    string    `json:"severity"`
	Stats       struct {
		Open     int `json:"open"`
		New      int `json:"new"`
		Resolved int `json:"resolved"`
	} `json:"stats"`
}

// analysisSeverities always get a series, so a panel shows 0 rather than
// no data for the others.
var analysisSeverities = []string{"success", "info", "warning", "error", "critical"}

// SetAnalysis sets the analysis gauges from a report.
func (m *Metrics) SetAnalysis(r Report) {
	m.analysisOpen.Set(float64(r.Stats.Open))
	m.analysisNew.Set(float64(r.Stats.New))
	m.analysisResolved.Set(float64(r.Stats.Resolved))
	m.analysisAt.Set(float64(r.GeneratedAt.Unix()))
	m.analysisSeverity.Reset()
	cur := strings.ToLower(r.Severity)
	known := false
	for _, s := range analysisSeverities {
		v := 0.0
		if s == cur {
			v, known = 1, true
		}
		m.analysisSeverity.WithLabelValues(s).Set(v)
	}
	if !known && cur != "" {
		m.analysisSeverity.WithLabelValues(cur).Set(1)
	}
}

// AnalysisWatcher keeps the analysis gauges on the newest report. It reads
// a report only when a newer one appears in the listing.
type AnalysisWatcher struct {
	Metrics *Metrics
	Getter  Getter
	lastKey string
}

// Update takes a listing of the analysis bucket, newest first (the
// inventory's), and loads the newest .json report if it changed.
func (w *AnalysisWatcher) Update(ctx context.Context, analyses []archive.Object) {
	key := newestJSON(analyses)
	if key == "" || key == w.lastKey {
		return
	}
	r, err := w.load(ctx, key)
	if err != nil {
		slog.Warn("analysis metrics not updated", "key", key, "error", err)
		return
	}
	w.Metrics.SetAnalysis(r)
	w.lastKey = key
	slog.Debug("analysis metrics updated", "key", key, "severity", r.Severity)
}

func (w *AnalysisWatcher) load(ctx context.Context, key string) (Report, error) {
	var r Report
	body, err := w.Getter.Get(ctx, key, maxReport)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return r, fmt.Errorf("decode %s: %w", key, err)
	}
	return r, nil
}

// newestJSON is the key of the newest .json object, "" if none.
func newestJSON(objs []archive.Object) string {
	var best archive.Object
	for _, o := range objs {
		if strings.HasSuffix(o.Key, ".json") && (best.Key == "" || o.LastModified.After(best.LastModified)) {
			best = o
		}
	}
	return best.Key
}
