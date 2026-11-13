package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stuttgart-things/homerun2-s3-catcher/internal/catcher"
	"github.com/stuttgart-things/homerun2-s3-catcher/internal/lag"
)

type fixedLag lag.Report

func (f fixedLag) Report() lag.Report { return lag.Report(f) }

func TestHealth(t *testing.T) {
	stats := &catcher.Stats{}
	stats.Archived.Add(3)
	stats.Missing.Add(1)

	for _, tc := range []struct {
		degraded bool
		want     string
	}{{false, "healthy"}, {true, "degraded"}} {
		h := NewHealthHandler(BuildInfo{Version: "v1"}, stats, fixedLag{Degraded: tc.degraded})
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodGet, "/healthz", http.NoBody))

		// Degraded is still 200: a probe must not restart a catcher that is
		// only behind.
		if rec.Code != http.StatusOK {
			t.Errorf("status code %d", rec.Code)
		}
		var body struct {
			Status   string
			Version  string
			Counters struct{ Archived, Missing int64 }
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Status != tc.want || body.Version != "v1" || body.Counters.Archived != 3 || body.Counters.Missing != 1 {
			t.Errorf("body = %+v", body)
		}
	}

	rec := httptest.NewRecorder()
	NewHealthHandler(BuildInfo{}, stats, nil)(rec, httptest.NewRequest(http.MethodPost, "/healthz", http.NoBody))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", rec.Code)
	}
}
