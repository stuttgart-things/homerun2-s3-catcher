// Package ui serves the archive's web UI: status, uploads per system and day,
// the latest objects and the analyses. Read-only; the catcher stays a sink.
package ui

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/yuin/goldmark"

	"github.com/stuttgart-things/homerun2-s3-catcher/internal/catcher"
	"github.com/stuttgart-things/homerun2-s3-catcher/internal/inventory"
	"github.com/stuttgart-things/homerun2-s3-catcher/internal/lag"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// Getter reads one object.
type Getter interface {
	Get(ctx context.Context, key string, maxBytes int64) ([]byte, error)
}

// LagReporter returns the latest lag measurement.
type LagReporter interface {
	Report() lag.Report
}

// BuildInfo holds version metadata.
type BuildInfo struct {
	Version, Commit, Date string
}

// UI serves the pages and fragments.
type UI struct {
	BasePath string
	Bucket   string
	Info     BuildInfo
	Stats    *catcher.Stats
	Lag      LagReporter
	Inv      *inventory.Inventory
	Archive  Getter
	Analysis Getter // nil without an analysis bucket
	Started  time.Time

	tmpl *template.Template
}

// maxObject bounds what the object view reads.
const maxObject = 1 << 20

const (
	tabStatus      = "status"
	keyBasePath    = "BasePath"
	hoursPerDay    = 24
	shortCommitLen = 7
)

// Handler returns the UI's routes, relative to the base path.
func (u *UI) Handler() http.Handler {
	u.tmpl = template.Must(template.New("").Funcs(template.FuncMap{
		"bytes":       func(n int) string { return humanBytes(int64(n)) },
		"bytes64":     humanBytes,
		"ago":         ago,
		"shortCommit": shortCommit,
	}).ParseFS(templateFS, "templates/*.html"))

	mux := http.NewServeMux()
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	mux.HandleFunc("GET /{$}", u.index)
	mux.HandleFunc("GET /ui/status", u.page("status.html", u.statusData))
	mux.HandleFunc("GET /ui/uploads", u.page("uploads.html", u.invData))
	mux.HandleFunc("GET /ui/objects", u.page("objects.html", u.invData))
	mux.HandleFunc("GET /ui/analyses", u.page("analyses.html", u.invData))
	mux.HandleFunc("GET /ui/object", u.object)
	mux.HandleFunc("GET /ui/analysis", u.analysis)
	mux.HandleFunc("GET /ui/manifest.json", u.manifest)
	mux.HandleFunc("GET /ui/fragment/status", u.page("fragment-status.html", u.statusData))
	return mux
}

type tab struct{ ID, Title string }

func (u *UI) tabs() []tab {
	t := []tab{{tabStatus, "Status"}, {"uploads", "Uploads"}, {"objects", "Objects"}}
	return append(t, tab{"analyses", "Analyses"})
}

func (u *UI) index(w http.ResponseWriter, r *http.Request) {
	active := r.URL.Query().Get("tab")
	valid := false
	for _, t := range u.tabs() {
		valid = valid || t.ID == active
	}
	if !valid {
		active = tabStatus
	}
	u.render(w, "index.html", map[string]any{
		keyBasePath: u.BasePath, "Bucket": u.Bucket, "Tabs": u.tabs(), "Tab": active,
		"Version": u.Info.Version, "Commit": u.Info.Commit, "Date": u.Info.Date,
		// Through homerun2-portal (it sets X-Forwarded-Prefix): link its tokens.
		"PortalTheme": r.Header.Get("X-Forwarded-Prefix") != "",
	})
}

func (u *UI) page(name string, data func(*http.Request) map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d := data(r)
		d[keyBasePath] = u.BasePath
		u.render(w, name, d)
	}
}

type streamRow struct {
	Name    string
	Pending int64
	Oldest  *time.Time
	Age     string
	Over    bool
	Error   string
}

func (u *UI) statusData(*http.Request) map[string]any {
	rep := lag.Report{}
	if u.Lag != nil {
		rep = u.Lag.Report()
	}
	maxLag, _ := time.ParseDuration(rep.MaxLag)
	var rows []streamRow
	maxAge := "0s"
	var maxD time.Duration
	for name, s := range rep.Streams {
		d, _ := time.ParseDuration(s.Age)
		rows = append(rows, streamRow{Name: name, Pending: s.Pending, Oldest: s.Oldest, Age: s.Age,
			Over: maxLag > 0 && d > maxLag, Error: s.Error})
		if d > maxD {
			maxD, maxAge = d, s.Age
		}
	}
	sort.Slice(rows, func(a, b int) bool { return rows[a].Name < rows[b].Name })
	status := "healthy"
	if rep.Degraded {
		status = "degraded"
	}
	return map[string]any{
		"Status": status, "Archived": u.Stats.Archived.Load(), "Failed": u.Stats.Failed.Load(),
		"Missing": u.Stats.Missing.Load(), "Skipped": u.Stats.Skipped.Load(), "Invalid": u.Stats.Invalid.Load(),
		"LastArchived": u.Stats.LastArchived(), "Started": u.Started, "MaxLag": rep.MaxLag,
		"Streams": rows, "Recent": u.Stats.Recent(), "MaxAge": maxAge,
	}
}

func (u *UI) invData(*http.Request) map[string]any {
	return map[string]any{"Inv": u.Inv.Snapshot()}
}

func (u *UI) object(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	d := map[string]any{keyBasePath: u.BasePath, "Key": key, "Max": maxObject}
	body, err := u.Archive.Get(r.Context(), key, maxObject+1)
	if err != nil {
		d["Error"] = err.Error()
	} else {
		d["Truncated"] = len(body) > maxObject
		if len(body) > maxObject {
			body = body[:maxObject]
		}
		d["Body"] = prettyJSON(body)
	}
	u.render(w, "object.html", d)
}

// md renders the analyses' Markdown. goldmark's default renderer drops raw
// HTML, so a report cannot inject markup into the page.
var md = goldmark.New()

func (u *UI) analysis(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	d := map[string]any{keyBasePath: u.BasePath, "Key": key}
	if u.Analysis == nil {
		d["Error"] = "no analysis bucket configured"
		u.render(w, "analysis.html", d)
		return
	}
	body, err := u.Analysis.Get(r.Context(), key, maxObject)
	switch {
	case err != nil:
		d["Error"] = err.Error()
	case strings.HasSuffix(key, ".md"):
		var b bytes.Buffer
		if err := md.Convert(body, &b); err != nil {
			d["Error"] = err.Error()
		} else {
			d["HTML"] = template.HTML(b.String()) //nolint:gosec // goldmark without raw HTML, see md
		}
	default:
		d["Body"] = prettyJSON(body)
	}
	u.render(w, "analysis.html", d)
}

func (u *UI) manifest(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"name": "homerun2-s3-catcher", "title": "Archive", "version": u.Info.Version, "basePath": u.BasePath,
		"fragments": []map[string]any{{
			"id": "status", "title": "Archive", "url": u.BasePath + "/ui/fragment/status", "refreshSeconds": 30,
		}},
	})
}

func (u *UI) render(w http.ResponseWriter, name string, data any) {
	var b bytes.Buffer
	if err := u.tmpl.ExecuteTemplate(&b, name, data); err != nil {
		slog.Error("render failed", "template", name, "error", err)
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = b.WriteTo(w)
}

func prettyJSON(b []byte) string {
	var out bytes.Buffer
	if err := json.Indent(&out, b, "", "  "); err != nil {
		return string(b)
	}
	return out.String()
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func ago(t time.Time) string {
	d := time.Since(t).Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 2*hoursPerDay*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/hoursPerDay))
	}
}

func shortCommit(c string) string {
	if len(c) > shortCommitLen {
		return c[:shortCommitLen]
	}
	return c
}
