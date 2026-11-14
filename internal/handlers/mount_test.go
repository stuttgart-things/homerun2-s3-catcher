package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMount(t *testing.T) {
	app := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("app:" + r.URL.Path)) })
	health := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("health")) })
	metrics := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("metrics")) })
	h := Mount("/archive", app, health, metrics)
	for path, want := range map[string]string{
		"/healthz":           "health",
		"/health":            "health",
		"/metrics":           "metrics",
		"/archive/ui/status": "app:/ui/status",
		"/archive/":          "app:/",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, http.NoBody))
		if rec.Body.String() != want {
			t.Errorf("%s: %q, want %q", path, rec.Body.String(), want)
		}
	}
	for _, path := range []string{"/", "/archive"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, http.NoBody))
		if rec.Code != http.StatusTemporaryRedirect || rec.Header().Get("Location") != "/archive/" {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Header().Get("Location"))
		}
	}
	rec := httptest.NewRecorder()
	root := Mount("", app, health, metrics)
	root.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/x", http.NoBody))
	if rec.Body.String() != "app:/ui/x" {
		t.Errorf("no base path: %q", rec.Body.String())
	}
	rec = httptest.NewRecorder()
	root.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody))
	if rec.Body.String() != "metrics" {
		t.Errorf("no base path, /metrics: %q", rec.Body.String())
	}
}

// A second UI under its own prefix (the portal's Analysis entry).
func TestMountExtra(t *testing.T) {
	app := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("app:" + r.URL.Path)) })
	an := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("analysis:" + r.URL.Path)) })
	h := Mount("/archive", app, app, nil, Prefixed{Path: "/analysis", Handler: an})
	for path, want := range map[string]string{"/analysis/": "analysis:/", "/analysis/ui/analyses": "analysis:/ui/analyses", "/archive/": "app:/"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, http.NoBody))
		if rec.Body.String() != want {
			t.Errorf("%s: %q, want %q", path, rec.Body.String(), want)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/analysis", http.NoBody))
	if rec.Code != http.StatusTemporaryRedirect || rec.Header().Get("Location") != "/analysis/" {
		t.Errorf("/analysis: %d %q", rec.Code, rec.Header().Get("Location"))
	}
}
