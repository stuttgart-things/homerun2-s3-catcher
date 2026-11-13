// Package lag measures how far the archive is behind the streams: the age of
// the oldest entry that is not archived yet. homerun2-scout trims streams and
// documents by age (SCOUT_RETENTION_TTL), so an archive further behind than
// that loses messages.
package lag

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Stream is the lag of one stream.
type Stream struct {
	Pending int64 `json:"pending"` // delivered, not acknowledged
	// Oldest is the time of the oldest unarchived entry, nil if none.
	Oldest *time.Time `json:"oldest,omitempty"`
	// Age is now minus Oldest, rounded to seconds; 0 if none.
	Age   string `json:"age"`
	Error string `json:"error,omitempty"`
	age   time.Duration
}

// Report is the latest measurement.
type Report struct {
	MeasuredAt time.Time         `json:"measuredAt"`
	MaxLag     string            `json:"maxLag"`
	Degraded   bool              `json:"degraded"`
	Streams    map[string]Stream `json:"streams"`
}

// Monitor measures the lag of a consumer group periodically.
type Monitor struct {
	client  redis.UniversalClient
	group   string
	streams []string
	maxLag  time.Duration
	now     func() time.Time

	mu     sync.RWMutex
	report Report
}

// New returns a monitor; maxLag 0 never reports degraded.
func New(client redis.UniversalClient, group string, streams []string, maxLag time.Duration) *Monitor {
	return &Monitor{client: client, group: group, streams: streams, maxLag: maxLag, now: time.Now}
}

// Report returns the latest measurement.
func (m *Monitor) Report() Report {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.report
}

// Run measures every interval until ctx is done.
func (m *Monitor) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		m.Measure(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Measure takes one measurement, stores it and logs a warning per stream
// whose lag exceeds the maximum.
func (m *Monitor) Measure(ctx context.Context) Report {
	now := m.now().UTC()
	r := Report{MeasuredAt: now, MaxLag: m.maxLag.String(), Streams: make(map[string]Stream, len(m.streams))}
	for _, s := range m.streams {
		st, err := m.stream(ctx, s, now)
		if err != nil {
			st.Error = err.Error()
			slog.Warn("lag measurement failed", "stream", s, "error", err)
		}
		if m.maxLag > 0 && st.age > m.maxLag {
			r.Degraded = true
			slog.Warn("archive lag above MAX_LAG: messages may be pruned before they are archived",
				"stream", s, "age", st.Age, "max_lag", m.maxLag.String(), "pending", st.Pending)
		}
		r.Streams[s] = st
	}
	m.mu.Lock()
	m.report = r
	m.mu.Unlock()
	return r
}

func (m *Monitor) stream(ctx context.Context, stream string, now time.Time) (Stream, error) {
	st := Stream{Age: "0s"}

	groups, err := m.client.XInfoGroups(ctx, stream).Result()
	if err != nil {
		if strings.Contains(err.Error(), "no such key") {
			return st, nil // stream not created yet: nothing to archive
		}
		return st, fmt.Errorf("XINFO GROUPS: %w", err)
	}
	var lastDelivered string
	found := false
	for _, g := range groups {
		if g.Name == m.group {
			lastDelivered, st.Pending, found = g.LastDeliveredID, g.Pending, true
		}
	}

	var oldest time.Time
	consider := func(id string) {
		if t, ok := idTime(id); ok && (oldest.IsZero() || t.Before(oldest)) {
			oldest = t
		}
	}

	if !found {
		// The group does not exist yet: everything in the stream is behind.
		first, err := m.client.XRangeN(ctx, stream, "-", "+", 1).Result()
		if err != nil {
			return st, fmt.Errorf("XRANGE: %w", err)
		}
		if len(first) > 0 {
			consider(first[0].ID)
		}
	} else {
		if st.Pending > 0 {
			p, err := m.client.XPending(ctx, stream, m.group).Result()
			if err != nil && !errors.Is(err, redis.Nil) {
				return st, fmt.Errorf("XPENDING: %w", err)
			}
			if p != nil {
				consider(p.Lower)
			}
		}
		next, err := m.client.XRangeN(ctx, stream, "("+lastDelivered, "+", 1).Result()
		if err != nil {
			return st, fmt.Errorf("XRANGE: %w", err)
		}
		if len(next) > 0 {
			consider(next[0].ID)
		}
	}

	if !oldest.IsZero() {
		st.Oldest = &oldest
		st.age = max(now.Sub(oldest), 0)
		st.Age = st.age.Round(time.Second).String()
	}
	return st, nil
}

// idTime is the time encoded in a stream ID ("<ms>-<seq>").
func idTime(id string) (time.Time, bool) {
	ms, _, _ := strings.Cut(id, "-")
	n, err := strconv.ParseInt(ms, 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}, false
	}
	return time.UnixMilli(n).UTC(), true
}
