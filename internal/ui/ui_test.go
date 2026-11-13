package ui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stuttgart-things/homerun2-s3-catcher/internal/archive"
	"github.com/stuttgart-things/homerun2-s3-catcher/internal/catcher"
	"github.com/stuttgart-things/homerun2-s3-catcher/internal/inventory"
	"github.com/stuttgart-things/homerun2-s3-catcher/internal/lag"
)

type store struct{ objs map[string]string }

func (s store) List(_ context.Context, prefix string) ([]archive.Object, error) {
	var out []archive.Object
	for k, v := range s.objs {
		if strings.HasPrefix(k, prefix) {
			out = append(out, archive.Object{Key: k, Size: int64(len(v)), LastModified: time.Now().Add(-time.Minute)})
		}
	}
	return out, nil
}

func (s store) Get(_ context.Context, key string, _ int64) ([]byte, error) {
	v, ok := s.objs[key]
	if !ok {
		return nil, errors.New("NoSuchKey")
	}
	return []byte(v), nil
}

type fixedLag lag.Report

func (f fixedLag) Report() lag.Report { return lag.Report(f) }

const key = "system=k8sgpt-a/date=" + "2026-11-13/abc.json"

func newUI(t *testing.T, withAnalysis bool) http.Handler {
	t.Helper()
	day := time.Now().UTC().Format("2006-01-02")
	arch := store{objs: map[string]string{
		key:                                   `{"messageID":"abc","message":{"title":"t"}}`,
		"system=flux/date=" + day + "/x.json": `{}`,
	}}
	an := store{objs: map[string]string{
		"analysis/date=2026-11-13/run.md":   "# Daily\n\n<script>alert(1)</script>\n\n- one finding",
		"analysis/date=2026-11-13/run.json": `{"topics":[]}`,
	}}
	var anL inventory.Lister
	var anG Getter
	if withAnalysis {
		anL, anG = an, an
	}
	inv := inventory.New(arch, anL, 14)
	inv.Refresh(context.Background())
	u := &UI{BasePath: "/archive", Bucket: "k8s-cluster-report", Info: BuildInfo{Version: "v0.2.0", Commit: "abcdef0123"},
		Stats: &catcher.Stats{}, Inv: inv, Archive: arch, Analysis: anG, Started: time.Now(),
		Lag: fixedLag{MaxLag: "12h", Degraded: true, Streams: map[string]lag.Stream{"k8sgpt": {Pending: 2, Age: "13h0m0s"}}}}
	return u.Handler()
}

func get(t *testing.T, h http.Handler, path string, hdr ...string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	if len(hdr) == 2 {
		req.Header.Set(hdr[0], hdr[1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func TestPages(t *testing.T) {
	h := newUI(t, true)
	for path, want := range map[string][]string{
		"/":                     {"Archive · k8s-cluster-report", `hx-get="/archive/ui/status"`, "abcdef0", "Analyses"},
		"/?tab=uploads":         {`hx-get="/archive/ui/uploads" hx-trigger="load"`},
		"/ui/status":            {"degraded", "k8sgpt", "13h0m0s", "Nothing uploaded since the start."},
		"/ui/uploads":           {"k8sgpt-a", "flux", "2 objects"},
		"/ui/objects":           {"key=system%3Dk8sgpt-a%2Fdate%3D2026-11-13%2Fabc.json"},
		"/ui/object?key=" + key: {"&#34;messageID&#34;: &#34;abc&#34;"},
		"/ui/analyses":          {"analysis/date=2026-11-13/run.md", "run.json"},
		"/ui/fragment/status":   {"hr-kpi", "13h0m0s", `href="/archive/"`},
	} {
		body := get(t, h, path)
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Errorf("GET %s: missing %q", path, w)
			}
		}
	}
}

// Reports are Markdown from the analysis bucket: rendered, without raw HTML.
func TestAnalysisMarkdownDropsRawHTML(t *testing.T) {
	body := get(t, newUI(t, true), "/ui/analysis?key=analysis/date=2026-11-13/run.md")
	if !strings.Contains(body, "<h1>Daily</h1>") || !strings.Contains(body, "<li>one finding</li>") {
		t.Errorf("markdown not rendered: %s", body)
	}
	if strings.Contains(body, "<script>") {
		t.Error("raw HTML from the report reached the page")
	}
}

func TestNoAnalysisBucket(t *testing.T) {
	h := newUI(t, false)
	if body := get(t, h, "/ui/analyses"); !strings.Contains(body, "No analysis bucket configured") {
		t.Errorf("analyses without bucket: %s", body)
	}
	if body := get(t, h, "/ui/analysis?key=x.md"); !strings.Contains(body, "no analysis bucket configured") {
		t.Errorf("analysis without bucket: %s", body)
	}
}

func TestObjectMissing(t *testing.T) {
	if body := get(t, newUI(t, true), "/ui/object?key=nope"); !strings.Contains(body, "NoSuchKey") {
		t.Errorf("missing object: %s", body)
	}
}

func TestPortalThemeAndManifest(t *testing.T) {
	h := newUI(t, false)
	if body := get(t, h, "/"); strings.Contains(body, "homerun2-tokens.css") {
		t.Error("portal tokens linked without the portal")
	}
	if body := get(t, h, "/", "X-Forwarded-Prefix", "/archive"); !strings.Contains(body, "/static/homerun2-tokens.css") {
		t.Error("portal tokens not linked behind the portal")
	}
	var m struct {
		Name      string
		Fragments []struct{ URL string }
	}
	if err := json.Unmarshal([]byte(get(t, h, "/ui/manifest.json")), &m); err != nil {
		t.Fatal(err)
	}
	if m.Name != "homerun2-s3-catcher" || m.Fragments[0].URL != "/archive/ui/fragment/status" {
		t.Errorf("manifest %+v", m)
	}
}

func TestHumanBytes(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1536: "1.5 KiB", 5 << 20: "5.0 MiB"} {
		if got := humanBytes(n); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", n, got, want)
		}
	}
}
