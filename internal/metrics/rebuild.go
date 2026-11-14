package metrics

import (
	"context"
	"log/slog"
	"sort"
	"strings"

	"github.com/stuttgart-things/homerun2-s3-catcher/internal/archive"
)

// ArchiveReader reads the archive bucket.
type ArchiveReader interface {
	// Prefixes lists the "directories" directly under prefix.
	Prefixes(ctx context.Context, prefix string) ([]string, error)
	List(ctx context.Context, prefix string) ([]archive.Object, error)
	Get(ctx context.Context, key string, maxBytes int64) ([]byte, error)
}

// RebuildOptions bound the reads per system.
type RebuildOptions struct {
	Days    int // newest date= partitions searched per system
	MaxGets int // objects read per system
}

// DefaultRebuildOptions: snapshots come at least hourly, so the newest one
// is among the newest objects of the newest day.
var DefaultRebuildOptions = RebuildOptions{Days: 2, MaxGets: 25}

// maxSnapshot bounds what is read of one archived object.
const maxSnapshot = 1 << 20

// Rebuild sets the snapshot gauges from the archive, so a restart does not
// zero the dashboards: per system= prefix the newest archived snapshot,
// searched newest day first and newest object first. A snapshot that
// arrived live in the meantime wins (Apply ignores older ones). It returns
// the number of systems restored.
func (m *Metrics) Rebuild(ctx context.Context, r ArchiveReader, o RebuildOptions) int {
	prefixes, err := r.Prefixes(ctx, "")
	if err != nil {
		slog.Warn("snapshot gauges not rebuilt", "error", err)
		return 0
	}
	restored := 0
	for _, p := range prefixes {
		if !strings.HasPrefix(p, "system=") {
			continue
		}
		s, ok, err := latestSnapshot(ctx, r, p, o)
		if err != nil {
			slog.Warn("snapshot gauges not rebuilt for system", "prefix", p, "error", err)
		}
		if ok && m.Apply(s) {
			restored++
		}
		if ctx.Err() != nil {
			break
		}
	}
	slog.Info("snapshot gauges rebuilt from the archive", "systems", restored)
	return restored
}

// latestSnapshot finds the newest snapshot under a system= prefix.
func latestSnapshot(ctx context.Context, r ArchiveReader, systemPrefix string, o RebuildOptions) (Snapshot, bool, error) {
	days, err := r.Prefixes(ctx, systemPrefix)
	if err != nil {
		return Snapshot{}, false, err
	}
	days = filterPrefix(days, systemPrefix+"date=")
	sort.Sort(sort.Reverse(sort.StringSlice(days)))
	if len(days) > o.Days {
		days = days[:o.Days]
	}
	gets := 0
	for _, day := range days {
		objs, err := r.List(ctx, day)
		if err != nil {
			return Snapshot{}, false, err
		}
		sort.Slice(objs, func(a, b int) bool { return objs[a].LastModified.After(objs[b].LastModified) })
		for _, obj := range objs {
			if gets == o.MaxGets {
				return Snapshot{}, false, nil
			}
			gets++
			body, err := r.Get(ctx, obj.Key, maxSnapshot)
			if err != nil {
				return Snapshot{}, false, err
			}
			if s, ok := ParseRecord(body); ok {
				return s, true, nil
			}
		}
	}
	return Snapshot{}, false, nil
}

func filterPrefix(vals []string, prefix string) []string {
	var out []string
	for _, v := range vals {
		if strings.HasPrefix(v, prefix) {
			out = append(out, v)
		}
	}
	return out
}
