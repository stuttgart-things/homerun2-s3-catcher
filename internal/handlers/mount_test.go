package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMount(t *testing.T) {
	app := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("app:" + r.URL.Path)) })
	health := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("health")) })
	h := Mount("/archive", app, health)
	for path, want := range map[string]string{
		"/healthz":           "health",
		"/health":            "health",
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
	Mount("", app, health).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/x", http.NoBody))
	if rec.Body.String() != "app:/ui/x" {
		t.Errorf("no base path: %q", rec.Body.String())
	}
}
