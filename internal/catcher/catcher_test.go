package catcher

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stuttgart-things/redisqueue"

	"github.com/stuttgart-things/homerun2-s3-catcher/internal/archive"
)

type fakeDocs struct {
	val string
	err error
}

func (f fakeDocs) Do(ctx context.Context, _ ...any) *redis.Cmd {
	c := redis.NewCmd(ctx)
	if f.err != nil {
		c.SetErr(f.err)
	} else {
		c.SetVal(f.val)
	}
	return c
}

type fakeArchiver struct {
	res archive.Result
	err error
	got []archive.Entry
}

func (f *fakeArchiver) Archive(_ context.Context, e archive.Entry) (archive.Result, error) {
	f.got = append(f.got, e)
	return f.res, f.err
}

type event struct{ kind, stream, detail string }

type recorder struct{ events []event }

func (r *recorder) Archived(stream, system string, _ []byte) {
	r.events = append(r.events, event{"archived", stream, system})
}
func (r *recorder) Failed(stream string) { r.events = append(r.events, event{"failed", stream, ""}) }
func (r *recorder) Unarchived(stream, reason string) {
	r.events = append(r.events, event{"unarchived", stream, reason})
}

func entry(values map[string]any) *redisqueue.Message {
	return &redisqueue.Message{ID: "1700000000000-0", Stream: "messages", Values: values}
}

// The return value of handle decides the acknowledgement: nil acks, an error
// leaves the entry pending for another delivery.
func TestHandle(t *testing.T) {
	for _, tc := range []struct {
		name    string
		values  map[string]any
		docs    fakeDocs
		arch    *fakeArchiver
		wantErr bool
		count   func(*Stats) int64
		event   event
	}{
		{"archived", map[string]any{"messageID": "m1"}, fakeDocs{val: `{"system":"k8s"}`},
			&fakeArchiver{res: archive.Result{Key: "k", System: "k8s"}}, false, func(s *Stats) int64 { return s.Archived.Load() },
			event{"archived", "messages", "k8s"}},
		{"skipped", map[string]any{"messageID": "m1"}, fakeDocs{val: `{}`},
			&fakeArchiver{res: archive.Result{Skipped: true}}, false, func(s *Stats) int64 { return s.Skipped.Load() },
			event{"unarchived", "messages", "skipped"}},
		{"document gone", map[string]any{"messageID": "m1"}, fakeDocs{err: redis.Nil},
			&fakeArchiver{}, false, func(s *Stats) int64 { return s.Missing.Load() },
			event{"unarchived", "messages", "missing"}},
		{"no messageID", map[string]any{"other": "x"}, fakeDocs{},
			&fakeArchiver{}, false, func(s *Stats) int64 { return s.Invalid.Load() },
			event{"unarchived", "messages", "invalid"}},
		{"invalid document", map[string]any{"messageID": "m1"}, fakeDocs{val: `x`},
			&fakeArchiver{err: fmt.Errorf("%w: decode", archive.ErrInvalid)}, false, func(s *Stats) int64 { return s.Invalid.Load() },
			event{"unarchived", "messages", "invalid"}},
		{"redis error", map[string]any{"messageID": "m1"}, fakeDocs{err: errors.New("connection refused")},
			&fakeArchiver{}, true, func(s *Stats) int64 { return s.Failed.Load() },
			event{"failed", "messages", ""}},
		{"upload error", map[string]any{"messageID": "m1"}, fakeDocs{val: `{}`},
			&fakeArchiver{err: errors.New("503 Slow Down")}, true, func(s *Stats) int64 { return s.Failed.Load() },
			event{"failed", "messages", ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stats := &Stats{}
			obs := &recorder{}
			c := &Catcher{docs: tc.docs, archiver: tc.arch, stats: stats, obs: obs, attempt: time.Second}
			err := c.handle(entry(tc.values))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got := tc.count(stats); got != 1 {
				t.Errorf("counter = %d, want 1 (stats %+v)", got, stats)
			}
			if len(obs.events) != 1 || obs.events[0] != tc.event {
				t.Errorf("observer events = %+v, want %+v", obs.events, tc.event)
			}
		})
	}
}

func TestHandlePassesEntry(t *testing.T) {
	a := &fakeArchiver{res: archive.Result{Key: "k"}}
	c := &Catcher{docs: fakeDocs{val: `{"title":"t"}`}, archiver: a, stats: &Stats{}, attempt: time.Second}
	if err := c.handle(entry(map[string]any{"messageID": "m1"})); err != nil {
		t.Fatal(err)
	}
	e := a.got[0]
	if e.Stream != "messages" || e.StreamID != "1700000000000-0" || e.MessageID != "m1" || string(e.Document) != `{"title":"t"}` {
		t.Errorf("entry = %+v", e)
	}
	if c.stats.LastArchived().IsZero() {
		t.Error("last archived not set")
	}
}

func TestNewValidates(t *testing.T) {
	if _, err := New(nil, nil, nil, &Stats{}, Options{StartID: "0"}); err == nil {
		t.Error("no streams accepted")
	}
	if _, err := New(nil, nil, nil, &Stats{}, Options{Streams: []string{"s"}, StartID: "latest"}); err == nil {
		t.Error("invalid start ID accepted")
	}
}

// An attempt ends before the entry is reclaimed, so a hanging upload is not
// started again in parallel.
func TestAttemptTimeout(t *testing.T) {
	for vt, want := range map[time.Duration]time.Duration{
		5 * time.Second:  2500 * time.Millisecond,
		time.Minute:      30 * time.Second,
		10 * time.Minute: 30 * time.Second,
		0:                30 * time.Second,
	} {
		if got := attemptTimeout(vt); got != want {
			t.Errorf("attemptTimeout(%v) = %v, want %v", vt, got, want)
		}
	}
}

// Recent keeps the newest uploads first, at most recentMax.
func TestRecent(t *testing.T) {
	s := &Stats{}
	for i := range recentMax + 5 {
		s.record(Upload{Key: fmt.Sprint(i)})
	}
	r := s.Recent()
	if len(r) != recentMax || r[0].Key != fmt.Sprint(recentMax+4) || r[len(r)-1].Key != "5" {
		t.Errorf("recent %d, first %s, last %s", len(r), r[0].Key, r[len(r)-1].Key)
	}
}
