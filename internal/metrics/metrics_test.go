package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/stuttgart-things/homerun2-s3-catcher/internal/archive"
	"github.com/stuttgart-things/homerun2-s3-catcher/internal/lag"
)

const v1Doc = `{
  "system": "endpoints-platform-sthings",
  "tags": "endpoints,snapshot",
  "severity": "warning",
  "timestamp": "2026-11-14T10:00:00Z",
  "payload": {
    "format": "findings/v1", "source": "endpoints", "target": "platform-sthings", "count": 3,
    "items": [
      {"fingerprint": "a", "kind": "Certificate", "name": "x", "severity": "warning"},
      {"fingerprint": "b", "kind": "Certificate", "name": "y", "severity": "Warning"},
      {"fingerprint": "c", "kind": "HTTPRoute", "name": "z", "severity": "critical"}
    ],
    "truncated": 2
  }
}`

const legacyDoc = `{
  "system": "k8sgpt-labda",
  "tags": "k8sgpt,snapshot",
  "timestamp_unix": 1794650400,
  "payload": {"count": 2, "items": [
    {"fingerprint": "f1", "object": {"kind": "Result", "spec": {"kind": "Pod", "name": "default/web-0"}}},
    {"fingerprint": "f2", "object": {"kind": "Result", "spec": {"kind": "Service"}}}
  ]}
}`

func TestParseMessageV1(t *testing.T) {
	s, ok := ParseMessage([]byte(v1Doc), time.Time{})
	if !ok {
		t.Fatal("not parsed")
	}
	if s.System != "endpoints-platform-sthings" || s.Source != "endpoints" || s.Target != "platform-sthings" {
		t.Errorf("labels %+v", s)
	}
	if !s.At.Equal(time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("at %v", s.At)
	}
	want := map[Series]int{{"Certificate", "warning"}: 2, {"HTTPRoute", "critical"}: 1}
	if fmt.Sprint(s.Counts) != fmt.Sprint(want) || s.Truncated != 2 || s.Open() != 3 {
		t.Errorf("counts %v truncated %d", s.Counts, s.Truncated)
	}
}

func TestParseMessageLegacy(t *testing.T) {
	s, ok := ParseMessage([]byte(legacyDoc), time.Time{})
	if !ok {
		t.Fatal("not parsed")
	}
	if s.Source != "k8sgpt" || s.Target != "labda" || s.At.Unix() != 1794650400 {
		t.Errorf("labels %+v", s)
	}
	want := map[Series]int{{"Pod", "warning"}: 1, {"Service", "warning"}: 1}
	if fmt.Sprint(s.Counts) != fmt.Sprint(want) {
		t.Errorf("counts %v", s.Counts)
	}
}

func TestParseMessageVariants(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, doc string
		ok        bool
		open      int
	}{
		{"empty legacy snapshot", `{"system":"k8sgpt-a","tags":"snapshot","payload":{"count":0,"items":[]}}`, true, 0},
		{"empty v1 snapshot", `{"system":"vm-h1","tags":"snapshot","payload":{"format":"findings/v1","items":[]}}`, true, 0},
		{"payload as string", `{"system":"vm-h1","tags":"snapshot","payload":"{\"format\":\"findings/v1\",\"items\":[{\"kind\":\"Disk\",\"severity\":\"error\"}]}"}`, true, 1},
		{"no snapshot tag", `{"system":"k8sgpt-a","tags":"k8sgpt,fp=abc","payload":{"format":"findings/v1","items":[]}}`, false, 0},
		{"tag substring only", `{"system":"k8sgpt-a","tags":"snapshots","payload":{"items":[]}}`, false, 0},
		{"inventory format", `{"system":"inventory-a","tags":"snapshot","payload":{"format":"inventory/v1","items":[]}}`, false, 0},
		{"legacy without items", `{"system":"k8sgpt-a","tags":"snapshot","payload":{"kind":"Result"}}`, false, 0},
		{"no payload", `{"system":"k8sgpt-a","tags":"snapshot"}`, false, 0},
		{"no system", `{"tags":"snapshot","payload":{"items":[]}}`, false, 0},
		{"not json", `x`, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, ok := ParseMessage([]byte(tc.doc), now)
			if ok != tc.ok || s.Open() != tc.open {
				t.Errorf("ok %v open %d, want %v %d", ok, s.Open(), tc.ok, tc.open)
			}
			if ok && !s.At.Equal(now) {
				t.Errorf("at %v, want now", s.At)
			}
		})
	}

	// findings/v1 without source/target: split from the system.
	s, _ := ParseMessage([]byte(`{"system":"vm-host-1","tags":"snapshot","payload":{"format":"findings/v1","items":[{"kind":"Disk"}]}}`), now)
	if s.Source != "vm" || s.Target != "host-1" || s.Counts[Series{"Disk", "unknown"}] != 1 {
		t.Errorf("fallback %+v", s)
	}
}

func TestParseRecord(t *testing.T) {
	body, _ := json.Marshal(archive.Record{MessageID: "m", ArchivedAt: "2026-11-14T11:00:00Z", Message: json.RawMessage(v1Doc)})
	s, ok := ParseRecord(body)
	if !ok || s.Open() != 3 {
		t.Errorf("record: ok %v %+v", ok, s)
	}
	if _, ok := ParseRecord([]byte(`{"message":{"system":"x"}}`)); ok {
		t.Error("non-snapshot record parsed")
	}
}

func snap(system string, at time.Time, counts map[Series]int) Snapshot {
	return Snapshot{System: system, Source: "endpoints", Target: strings.TrimPrefix(system, "endpoints-"), At: at, Counts: counts}
}

// A new snapshot replaces the system's series: resolved kinds disappear,
// other systems keep theirs, an older snapshot does not overwrite a newer.
func TestApplyResets(t *testing.T) {
	m := New(nil)
	t0 := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	m.Apply(snap("endpoints-a", t0, map[Series]int{{"Certificate", "warning"}: 2, {"HTTPRoute", "error"}: 1}))
	m.Apply(snap("endpoints-b", t0, map[Series]int{{"Certificate", "warning"}: 1}))
	if n := testutil.CollectAndCount(m.findings); n != 3 {
		t.Fatalf("series %d, want 3", n)
	}

	if !m.Apply(snap("endpoints-a", t0.Add(time.Hour), map[Series]int{{"Certificate", "warning"}: 1})) {
		t.Fatal("newer snapshot not applied")
	}
	if n := testutil.CollectAndCount(m.findings); n != 2 {
		t.Errorf("series %d after reset, want 2", n)
	}
	if v := testutil.ToFloat64(m.findings.WithLabelValues("endpoints-a", "endpoints", "a", "Certificate", "warning")); v != 1 {
		t.Errorf("a Certificate = %v", v)
	}
	if v := testutil.ToFloat64(m.snapshotAt.WithLabelValues("endpoints-a", "endpoints", "a")); v != float64(t0.Add(time.Hour).Unix()) {
		t.Errorf("timestamp %v", v)
	}

	if m.Apply(snap("endpoints-a", t0, map[Series]int{{"Pod", "warning"}: 5})) {
		t.Error("older snapshot applied")
	}

	// Everything resolved: no findings series left, the timestamp stays.
	m.Apply(snap("endpoints-a", t0.Add(2*time.Hour), map[Series]int{}))
	if n := testutil.CollectAndCount(m.findings); n != 1 {
		t.Errorf("series %d, want only b's", n)
	}
	if n := testutil.CollectAndCount(m.snapshotAt); n != 2 {
		t.Errorf("timestamp series %d", n)
	}
	if v := testutil.ToFloat64(m.open.WithLabelValues("endpoints-a", "endpoints", "a")); v != 0 {
		t.Errorf("open findings of a clean system = %v, want 0", v)
	}
	if v := testutil.ToFloat64(m.open.WithLabelValues("endpoints-b", "endpoints", "b")); v != 1 {
		t.Errorf("open findings of b = %v", v)
	}
	if n := testutil.CollectAndCount(m.open); n != 2 {
		t.Errorf("open series %d, want one per system", n)
	}
}

func TestArchivedAndHandler(t *testing.T) {
	lagr := fixedLag{Streams: map[string]lag.Stream{"messages": {Pending: 4}}}
	m := New(lagr)
	m.Archived("messages", "endpoints-platform-sthings", []byte(v1Doc))
	m.Archived("messages", "other", []byte(`{"system":"other","title":"t"}`))
	m.Failed("messages")
	m.Unarchived("messages", "skipped")

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody))
	body, _ := io.ReadAll(rec.Body)
	for _, want := range []string{
		`homerun_archived_messages_total{stream="messages",system="other"} 1`,
		`homerun_archive_errors_total{stream="messages"} 1`,
		`homerun_unarchived_messages_total{reason="skipped",stream="messages"} 1`,
		`homerun_findings{kind="Certificate",severity="warning",source="endpoints",system="endpoints-platform-sthings",target="platform-sthings"} 2`,
		`homerun_snapshot_open_findings{source="endpoints",system="endpoints-platform-sthings",target="platform-sthings"} 3`,
		`homerun_snapshot_truncated_findings{source="endpoints",system="endpoints-platform-sthings",target="platform-sthings"} 2`,
		`homerun_consumer_lag_seconds{stream="messages"} 0`,
		`homerun_consumer_pending{stream="messages"} 4`,
		`go_goroutines`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("missing %s", want)
		}
	}
}

type fixedLag lag.Report

func (f fixedLag) Report() lag.Report { return lag.Report(f) }

type fakeBucket struct {
	objs  map[string]archive.Object
	data  map[string][]byte
	reads []string
}

func (f *fakeBucket) put(key string, at time.Time, body string) {
	if f.objs == nil {
		f.objs, f.data = map[string]archive.Object{}, map[string][]byte{}
	}
	f.objs[key] = archive.Object{Key: key, LastModified: at}
	f.data[key] = []byte(body)
}

func (f *fakeBucket) Prefixes(_ context.Context, prefix string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for k := range f.objs {
		rest, ok := strings.CutPrefix(k, prefix)
		if !ok {
			continue
		}
		if dir, _, ok := strings.Cut(rest, "/"); ok && !seen[dir] {
			seen[dir] = true
			out = append(out, prefix+dir+"/")
		}
	}
	return out, nil
}

func (f *fakeBucket) List(_ context.Context, prefix string) ([]archive.Object, error) {
	var out []archive.Object
	for k, o := range f.objs {
		if strings.HasPrefix(k, prefix) {
			out = append(out, o)
		}
	}
	return out, nil
}

func (f *fakeBucket) Get(_ context.Context, key string, _ int64) ([]byte, error) {
	f.reads = append(f.reads, key)
	return f.data[key], nil
}

func record(doc string) string {
	b, _ := json.Marshal(archive.Record{MessageID: "m", Message: json.RawMessage(doc)})
	return string(b)
}

func TestRebuild(t *testing.T) {
	t0 := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	b := &fakeBucket{}
	// endpoints: an older snapshot yesterday, the newest today behind an event.
	b.put("system=endpoints-platform-sthings/date=2026-11-13/old.json", t0.Add(-24*time.Hour),
		record(`{"system":"endpoints-platform-sthings","tags":"snapshot","timestamp":"2026-11-13T10:00:00Z","payload":{"format":"findings/v1","items":[{"kind":"Pod","severity":"error"}]}}`))
	b.put("system=endpoints-platform-sthings/date=2026-11-14/snap.json", t0, record(v1Doc))
	b.put("system=endpoints-platform-sthings/date=2026-11-14/event.json", t0.Add(time.Minute),
		record(`{"system":"endpoints-platform-sthings","tags":"fp=a","payload":{"format":"findings/v1","items":[]}}`))
	// k8sgpt: the snapshot only on the previous day.
	b.put("system=k8sgpt-labda/date=2026-11-13/s.json", t0.Add(-time.Hour), record(legacyDoc))
	b.put("system=k8sgpt-labda/date=2026-11-14/e.json", t0, record(`{"system":"k8sgpt-labda","tags":"k8sgpt"}`))
	// a system without snapshots, and an object outside the layout.
	b.put("system=vm-h1/date=2026-11-14/e.json", t0, record(`{"system":"vm-h1"}`))
	b.put("analysis/x.json", t0, `{}`)

	m := New(nil)
	if n := m.Rebuild(context.Background(), b, DefaultRebuildOptions); n != 2 {
		t.Fatalf("restored %d, want 2", n)
	}
	if v := testutil.ToFloat64(m.findings.WithLabelValues("endpoints-platform-sthings", "endpoints", "platform-sthings", "HTTPRoute", "critical")); v != 1 {
		t.Errorf("endpoints from today's snapshot: %v", v)
	}
	if n := testutil.CollectAndCount(m.findings); n != 4 {
		t.Errorf("series %d, want 2 endpoints + 2 k8sgpt", n)
	}

	// The read budget bounds the GETs per system.
	b.reads = nil
	m.Rebuild(context.Background(), b, RebuildOptions{Days: 2, MaxGets: 1})
	perSystem := map[string]int{}
	for _, k := range b.reads {
		sys, _, _ := strings.Cut(k, "/")
		perSystem[sys]++
	}
	for sys, n := range perSystem {
		if n > 1 {
			t.Errorf("%s: %d reads", sys, n)
		}
	}
}

type fakeGetter map[string]string

func (f fakeGetter) Get(_ context.Context, key string, _ int64) ([]byte, error) {
	if v, ok := f[key]; ok {
		return []byte(v), nil
	}
	return nil, fmt.Errorf("no such key %s", key)
}

func TestAnalysisWatcher(t *testing.T) {
	t0 := time.Date(2026, 11, 14, 6, 0, 0, 0, time.UTC)
	m := New(nil)
	g := fakeGetter{
		"analysis/date=2026-11-14/r2.json": `{"generatedAt":"2026-11-14T06:00:00Z","severity":"warning","stats":{"open":7,"new":2,"resolved":1}}`,
	}
	w := &AnalysisWatcher{Metrics: m, Getter: g}
	w.Update(context.Background(), []archive.Object{
		{Key: "analysis/date=2026-11-14/r2.md", LastModified: t0.Add(time.Second)},
		{Key: "analysis/date=2026-11-14/r2.json", LastModified: t0},
		{Key: "analysis/date=2026-11-13/r1.json", LastModified: t0.Add(-24 * time.Hour)},
	})
	if testutil.ToFloat64(m.analysisOpen) != 7 || testutil.ToFloat64(m.analysisNew) != 2 ||
		testutil.ToFloat64(m.analysisResolved) != 1 || testutil.ToFloat64(m.analysisAt) != float64(t0.Unix()) {
		t.Error("analysis gauges not set")
	}
	if testutil.ToFloat64(m.analysisSeverity.WithLabelValues("warning")) != 1 ||
		testutil.ToFloat64(m.analysisSeverity.WithLabelValues("success")) != 0 {
		t.Error("severity gauge")
	}

	// Same key: no new read (the getter would fail for a missing one).
	delete(g, "analysis/date=2026-11-14/r2.json")
	w.Update(context.Background(), []archive.Object{{Key: "analysis/date=2026-11-14/r2.json", LastModified: t0}})
	// A newer report that cannot be read keeps the old values.
	w.Update(context.Background(), []archive.Object{{Key: "analysis/date=2026-11-15/r3.json", LastModified: t0.Add(24 * time.Hour)}})
	if testutil.ToFloat64(m.analysisOpen) != 7 {
		t.Error("failed read changed the gauges")
	}
}
