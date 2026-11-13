package inventory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stuttgart-things/homerun2-s3-catcher/internal/archive"
)

type fakeLister struct {
	objs []archive.Object
	err  error
}

func (f *fakeLister) List(_ context.Context, prefix string) ([]archive.Object, error) {
	return f.objs, f.err
}

var now = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

func obj(key string, size int64, ago time.Duration) archive.Object {
	return archive.Object{Key: key, Size: size, LastModified: now.Add(-ago)}
}

func TestAggregate(t *testing.T) {
	l := &fakeLister{objs: []archive.Object{
		obj("system=k8sgpt-a/date=2026-11-13/1.json", 100, time.Minute),
		obj("system=k8sgpt-a/date=2026-11-13/2.json", 200, 2*time.Minute),
		obj("system=flux/date=2026-11-12/3.json", 50, 25*time.Hour),
		obj("system=flux/date=2026-10-01/old.json", 10, 900*time.Hour), // outside 14 days
		obj("custom/layout.json", 5, 3*time.Hour),                      // no system/date
	}}
	inv := New(l, nil, 14)
	inv.now = func() time.Time { return now }
	inv.Refresh(context.Background())
	s := inv.Snapshot()

	if s.Objects != 5 || s.Bytes != 365 || s.UnknownCount != 1 {
		t.Errorf("totals %d objects %d bytes %d unknown", s.Objects, s.Bytes, s.UnknownCount)
	}
	if s.Days[0] != "2026-11-13" || len(s.Days) != 14 {
		t.Errorf("days %v", s.Days)
	}
	if c := s.Cells["k8sgpt-a"]["2026-11-13"]; c.Objects != 2 || c.Bytes != 300 {
		t.Errorf("cell %+v", c)
	}
	if _, ok := s.Cells["flux"]["2026-10-01"]; ok {
		t.Error("day outside the range counted in the table")
	}
	if c := s.Cells["(other)"]["2026-11-13"]; c.Objects != 1 {
		t.Errorf("key without system/date: %+v", s.Cells["(other)"])
	}
	if s.DayTotals["2026-11-13"].Objects != 3 {
		t.Errorf("day total %+v", s.DayTotals["2026-11-13"])
	}
	if s.Latest[0].Key != "system=k8sgpt-a/date=2026-11-13/1.json" {
		t.Errorf("latest %v", s.Latest[0].Key)
	}
	if s.HasAnalyses {
		t.Error("analyses without an analysis bucket")
	}
}

// A failing listing keeps the previous numbers and says why.
func TestRefreshErrorKeepsLastSnapshot(t *testing.T) {
	l := &fakeLister{objs: []archive.Object{obj("system=a/date=2026-11-13/1.json", 1, time.Minute)}}
	inv := New(l, nil, 14)
	inv.now = func() time.Time { return now }
	inv.Refresh(context.Background())
	l.err = errors.New("connection refused")
	inv.Refresh(context.Background())
	s := inv.Snapshot()
	if s.Objects != 1 || s.Err != "connection refused" {
		t.Errorf("objects %d err %q", s.Objects, s.Err)
	}
}

func TestAnalyses(t *testing.T) {
	an := &fakeLister{objs: []archive.Object{
		obj("analysis/date=2026-11-12/run1.md", 1, 24*time.Hour),
		obj("analysis/date=2026-11-13/run2.md", 1, time.Hour),
		obj("analysis/date=2026-11-13/run2.json", 1, time.Hour),
		obj("analysis/date=2026-11-13/input.tmp", 1, time.Hour),
	}}
	inv := New(&fakeLister{}, an, 14)
	inv.now = func() time.Time { return now }
	inv.Refresh(context.Background())
	s := inv.Snapshot()
	if !s.HasAnalyses || len(s.Analyses) != 3 || s.Analyses[2].Key != "analysis/date=2026-11-12/run1.md" {
		t.Errorf("analyses %+v", s.Analyses)
	}
}
