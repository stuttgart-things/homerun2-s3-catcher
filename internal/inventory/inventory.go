// Package inventory keeps a periodically refreshed view of what is in the
// archive bucket: objects and bytes per system and day, the latest objects,
// and the analyses. Listing a bucket on every page view would not scale, so
// the UI reads the last snapshot.
package inventory

import (
	"context"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/stuttgart-things/homerun2-s3-catcher/internal/archive"
)

// Lister lists the objects of one bucket.
type Lister interface {
	List(ctx context.Context, prefix string) ([]archive.Object, error)
}

// Cell is the volume of one system on one day.
type Cell struct {
	Objects int
	Bytes   int64
}

// Snapshot is one refresh of the inventory.
type Snapshot struct {
	RefreshedAt  time.Time
	Err          string
	Objects      int
	Bytes        int64
	Days         []string // newest first, at most the configured days
	Systems      []string // sorted
	Cells        map[string]map[string]Cell
	DayTotals    map[string]Cell
	Latest       []archive.Object // newest first
	Analyses     []archive.Object // .md and .json under analysis/, newest first
	AnalysisErr  string
	HasAnalyses  bool // an analysis bucket is configured
	UnknownCount int  // objects whose key names no system or date
}

// Inventory refreshes a Snapshot every interval.
type Inventory struct {
	archive  Lister
	analysis Lister // nil without an analysis bucket
	days     int
	latest   int
	now      func() time.Time

	mu   sync.RWMutex
	snap Snapshot
}

// New returns an inventory of the archive bucket and, if analysis is not
// nil, the analyses; days bounds the per-day table.
func New(archiveBucket, analysis Lister, days int) *Inventory {
	if days < 1 {
		days = 14
	}
	return &Inventory{archive: archiveBucket, analysis: analysis, days: days, latest: 50, now: time.Now}
}

// Snapshot returns the last refresh.
func (i *Inventory) Snapshot() Snapshot {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.snap
}

// Run refreshes every interval until ctx is done.
func (i *Inventory) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		i.Refresh(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

var (
	systemPart = regexp.MustCompile(`(?:^|/)system=([^/]+)/`)
	datePart   = regexp.MustCompile(`(?:^|/)date=(\d{4}-\d{2}-\d{2})/`)
)

// Refresh lists the buckets once. A failing listing keeps the previous
// numbers and reports the error.
func (i *Inventory) Refresh(ctx context.Context) {
	objs, err := i.archive.List(ctx, "")
	snap := i.Snapshot()
	snap.RefreshedAt = i.now().UTC()
	if err != nil {
		snap.Err = err.Error()
		slog.Warn("inventory refresh failed", "error", err)
	} else {
		snap = i.aggregate(objs, snap.RefreshedAt)
	}

	snap.HasAnalyses = i.analysis != nil
	if i.analysis != nil {
		an, err := i.analysis.List(ctx, "analysis/")
		if err != nil {
			snap.AnalysisErr = err.Error()
		} else {
			snap.AnalysisErr = ""
			snap.Analyses = newestFirst(filterSuffix(an, ".md", ".json"), 100)
		}
	}

	i.mu.Lock()
	i.snap = snap
	i.mu.Unlock()
}

func (i *Inventory) aggregate(objs []archive.Object, at time.Time) Snapshot {
	s := Snapshot{
		RefreshedAt: at,
		Cells:       map[string]map[string]Cell{},
		DayTotals:   map[string]Cell{},
	}
	var days []string
	for d := 0; d < i.days; d++ {
		days = append(days, at.AddDate(0, 0, -d).Format("2006-01-02"))
	}
	inRange := map[string]bool{}
	for _, d := range days {
		inRange[d] = true
	}
	systems := map[string]bool{}
	for _, o := range objs {
		s.Objects++
		s.Bytes += o.Size
		system, day := "", ""
		if m := systemPart.FindStringSubmatch(o.Key); m != nil {
			system = m[1]
		}
		if m := datePart.FindStringSubmatch(o.Key); m != nil {
			day = m[1]
		}
		if system == "" || day == "" {
			s.UnknownCount++
			if system == "" {
				system = "(other)"
			}
			if day == "" {
				day = o.LastModified.UTC().Format("2006-01-02")
			}
		}
		if !inRange[day] {
			continue
		}
		if s.Cells[system] == nil {
			s.Cells[system] = map[string]Cell{}
		}
		c := s.Cells[system][day]
		c.Objects++
		c.Bytes += o.Size
		s.Cells[system][day] = c
		t := s.DayTotals[day]
		t.Objects++
		t.Bytes += o.Size
		s.DayTotals[day] = t
		systems[system] = true
	}
	s.Days = days
	for sys := range systems {
		s.Systems = append(s.Systems, sys)
	}
	sort.Strings(s.Systems)
	s.Latest = newestFirst(objs, i.latest)
	return s
}

func newestFirst(objs []archive.Object, n int) []archive.Object {
	out := append([]archive.Object(nil), objs...)
	sort.Slice(out, func(a, b int) bool { return out[a].LastModified.After(out[b].LastModified) })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func filterSuffix(objs []archive.Object, suffixes ...string) []archive.Object {
	var out []archive.Object
	for _, o := range objs {
		for _, s := range suffixes {
			if strings.HasSuffix(o.Key, s) {
				out = append(out, o)
				break
			}
		}
	}
	return out
}
