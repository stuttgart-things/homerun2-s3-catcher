// Package handlers serves the health endpoints.
package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/stuttgart-things/homerun2-s3-catcher/internal/catcher"
	"github.com/stuttgart-things/homerun2-s3-catcher/internal/lag"
)

// BuildInfo holds version metadata.
type BuildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

// LagReporter returns the latest lag measurement.
type LagReporter interface {
	Report() lag.Report
}

type counters struct {
	Archived     int64  `json:"archived"`
	Skipped      int64  `json:"skipped"`
	Missing      int64  `json:"missing"`
	Invalid      int64  `json:"invalid"`
	Failed       int64  `json:"failed"`
	LastArchived string `json:"lastArchived,omitempty"`
}

type health struct {
	Status string `json:"status"` // healthy or degraded
	Time   string `json:"time"`
	BuildInfo
	Counters counters   `json:"counters"`
	Lag      lag.Report `json:"lag"`
}

// NewHealthHandler serves GET /health and /healthz: status, counters and the
// archive lag. A lag above MAX_LAG is "degraded" with HTTP 200, so a probe
// does not restart a catcher that is merely behind; alert on the status.
func NewHealthHandler(info BuildInfo, stats *catcher.Stats, lagr LagReporter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h := health{
			Status:    "healthy",
			Time:      time.Now().UTC().Format(time.RFC3339),
			BuildInfo: info,
			Counters: counters{
				Archived: stats.Archived.Load(),
				Skipped:  stats.Skipped.Load(),
				Missing:  stats.Missing.Load(),
				Invalid:  stats.Invalid.Load(),
				Failed:   stats.Failed.Load(),
			},
		}
		if t := stats.LastArchived(); !t.IsZero() {
			h.Counters.LastArchived = t.Format(time.RFC3339)
		}
		if lagr != nil {
			h.Lag = lagr.Report()
			if h.Lag.Degraded {
				h.Status = "degraded"
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(h)
	}
}
