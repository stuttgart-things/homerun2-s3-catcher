package archive

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type memStore struct {
	objects map[string][]byte
	err     error
}

func (m *memStore) Put(_ context.Context, key string, body []byte) error {
	if m.err != nil {
		return m.err
	}
	if m.objects == nil {
		m.objects = map[string][]byte{}
	}
	m.objects[key] = body
	return nil
}

var fixedNow = time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)

func newArchiver(t *testing.T, store Store, tmpl string, systems ...string) *Archiver {
	t.Helper()
	a, err := New(store, tmpl, systems)
	if err != nil {
		t.Fatal(err)
	}
	a.now = func() time.Time { return fixedNow }
	return a
}

const defaultTemplate = "system={{.System}}/date={{.Date}}/{{.MessageID}}.json"

func TestArchiveWritesRecord(t *testing.T) {
	store := &memStore{}
	a := newArchiver(t, store, defaultTemplate)
	doc := `{"title":"Pod default/web-0: 1 finding","system":"k8s-labda","timestamp":"2026-11-12T23:59:00Z",` +
		`"payload":{"spec":{"error":[{"text":"back-off"}]}},"timestamp_unix":1794527940}`

	res, err := a.Archive(context.Background(), Entry{Stream: "messages", StreamID: "1-0", MessageID: "abc-k8s-labda", Document: json.RawMessage(doc)})
	if err != nil {
		t.Fatal(err)
	}
	if want := "system=k8s-labda/date=2026-11-12/abc-k8s-labda.json"; res.Key != want {
		t.Errorf("key = %q, want %q", res.Key, want)
	}

	var rec struct {
		MessageID, Stream, StreamID, ArchivedAt string
		Message                                 map[string]any
	}
	if err := json.Unmarshal(store.objects[res.Key], &rec); err != nil {
		t.Fatal(err)
	}
	if rec.MessageID != "abc-k8s-labda" || rec.Stream != "messages" || rec.StreamID != "1-0" || rec.ArchivedAt != "2026-11-13T09:30:00Z" {
		t.Errorf("record = %+v", rec)
	}
	// The payload stays nested, not a string.
	errs := rec.Message["payload"].(map[string]any)["spec"].(map[string]any)["error"].([]any)
	if errs[0].(map[string]any)["text"] != "back-off" {
		t.Errorf("payload = %v", rec.Message["payload"])
	}
}

func TestKeyEventTime(t *testing.T) {
	a := newArchiver(t, &memStore{}, "{{.Date}}/{{.Hour}}")
	e := Entry{MessageID: "m"}
	for _, tc := range []struct {
		name string
		h    header
		want string
	}{
		{"timestamp_unix wins", header{TimestampUnix: 1794527940, Timestamp: "2020-01-01T00:00:00Z"}, "2026-11-12/23"},
		{"RFC3339 timestamp", header{Timestamp: "2026-11-01T05:00:00+02:00"}, "2026-11-01/03"},
		{"unparseable: archive time", header{Timestamp: "yesterday"}, "2026-11-13/09"},
	} {
		got, err := a.Key(e, tc.h, fixedNow)
		if err != nil || got != tc.want {
			t.Errorf("%s: key = %q, %v, want %q", tc.name, got, err, tc.want)
		}
	}
}

func TestKeySanitizesSegments(t *testing.T) {
	a := newArchiver(t, &memStore{}, "{{.System}}/{{.Severity}}/{{.MessageID}}")
	got, err := a.Key(Entry{MessageID: "../x/y"}, header{System: "a/b c", Severity: ""}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if got != "a_b_c/unknown/x_y" {
		t.Errorf("key = %q", got)
	}
	for _, v := range []string{"", ".", "..", "///"} {
		if s := segment(v); s != "unknown" {
			t.Errorf("segment(%q) = %q", v, s)
		}
	}
}

func TestArchiveFiltersSystems(t *testing.T) {
	store := &memStore{}
	a := newArchiver(t, store, defaultTemplate, "k8s-labda")
	res, err := a.Archive(context.Background(), Entry{MessageID: "m", Document: json.RawMessage(`{"system":"github"}`)})
	if err != nil || !res.Skipped || len(store.objects) != 0 {
		t.Errorf("res %+v err %v objects %d", res, err, len(store.objects))
	}
	res, err = a.Archive(context.Background(), Entry{MessageID: "m", Document: json.RawMessage(`{"system":"k8s-labda"}`)})
	if err != nil || res.Skipped || len(store.objects) != 1 {
		t.Errorf("res %+v err %v objects %d", res, err, len(store.objects))
	}
}

func TestArchiveErrors(t *testing.T) {
	a := newArchiver(t, &memStore{}, defaultTemplate)
	if _, err := a.Archive(context.Background(), Entry{MessageID: "m", Document: json.RawMessage(`not json`)}); !errors.Is(err, ErrInvalid) {
		t.Errorf("invalid document: err = %v, want ErrInvalid", err)
	}

	failing := newArchiver(t, &memStore{err: errors.New("503")}, defaultTemplate)
	_, err := failing.Archive(context.Background(), Entry{MessageID: "m", Document: json.RawMessage(`{}`)})
	if err == nil || errors.Is(err, ErrInvalid) {
		t.Errorf("store error: err = %v, want a retryable error", err)
	}
}

func TestNewRejectsBadTemplates(t *testing.T) {
	for _, tmpl := range []string{"{{.Nope}}", "{{", "/"} {
		if _, err := New(&memStore{}, tmpl, nil); err == nil {
			t.Errorf("template %q accepted", tmpl)
		}
	}
	if _, err := New(&memStore{}, "{{.Stream}}/{{.Year}}/{{.Month}}/{{.Day}}/{{.Severity}}/{{.MessageID}}", nil); err != nil {
		t.Errorf("all fields: %v", err)
	}
	if !strings.Contains(defaultTemplate, "{{.MessageID}}") {
		t.Error("default template must contain the messageID (idempotent keys)")
	}
}
