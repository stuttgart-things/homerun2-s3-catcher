// Package archive turns a homerun message into an S3 object: the key from a
// template, the body as a JSON envelope around the stored Redis JSON document.
package archive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"text/template"
	"time"
)

// Entry is one stream entry with the Redis JSON document it references.
type Entry struct {
	Stream    string
	StreamID  string
	MessageID string
	// Document is the message as JSON.GET returned it.
	Document json.RawMessage
}

// Record is the archived object. The document is kept as it was stored, so
// fields the catcher does not know (payload, timestamp_unix, later ones)
// survive unchanged.
type Record struct {
	MessageID  string          `json:"messageID"`
	Stream     string          `json:"stream"`
	StreamID   string          `json:"streamID"`
	ArchivedAt string          `json:"archivedAt"`
	Message    json.RawMessage `json:"message"`
}

// KeyFields are available in the key template.
type KeyFields struct {
	System    string // message system, "unknown" if empty
	Severity  string // lower case, "unknown" if empty
	Date      string // event date, 2006-01-02
	Year      string
	Month     string
	Day       string
	Hour      string
	MessageID string
	Stream    string
}

// ErrInvalid marks an entry that can never be archived (a document that is not
// a JSON object, a key that does not render); delivering it again is futile.
var ErrInvalid = errors.New("invalid entry")

// Store writes an object.
type Store interface {
	Put(ctx context.Context, key string, body []byte) error
}

// Archiver archives entries into a Store.
type Archiver struct {
	store   Store
	key     *template.Template
	systems map[string]bool
	now     func() time.Time
}

// New returns an Archiver writing to store. keyTemplate is a Go template over
// KeyFields; systems, if not empty, limits archiving to those systems.
func New(store Store, keyTemplate string, systems []string) (*Archiver, error) {
	tmpl, err := template.New("key").Option("missingkey=error").Parse(keyTemplate)
	if err != nil {
		return nil, fmt.Errorf("invalid key template %q: %w", keyTemplate, err)
	}
	// Render once with sample values, so a template naming an unknown field
	// fails at startup and not on the first message.
	if _, err := render(tmpl, KeyFields{System: "s", Severity: "info", Date: "2026-01-02", MessageID: "m", Stream: "s"}); err != nil {
		return nil, fmt.Errorf("invalid key template %q: %w", keyTemplate, err)
	}
	a := &Archiver{store: store, key: tmpl, now: time.Now}
	if len(systems) > 0 {
		a.systems = make(map[string]bool, len(systems))
		for _, s := range systems {
			a.systems[s] = true
		}
	}
	return a, nil
}

// Result says what Archive did with an entry.
type Result struct {
	Key     string // object key, empty if skipped
	Skipped bool   // filtered out by system
}

// header is the part of the document the key needs.
type header struct {
	System        string `json:"system"`
	Severity      string `json:"severity"`
	Timestamp     string `json:"timestamp"`
	TimestampUnix int64  `json:"timestamp_unix"`
}

// Archive writes e to the store, unless its system is filtered out. An error
// means nothing was archived and the entry should be delivered again.
func (a *Archiver) Archive(ctx context.Context, e Entry) (Result, error) {
	var h header
	if err := json.Unmarshal(e.Document, &h); err != nil {
		return Result{}, fmt.Errorf("%w: decode document %s: %w", ErrInvalid, e.MessageID, err)
	}
	if a.systems != nil && !a.systems[h.System] {
		return Result{Skipped: true}, nil
	}

	now := a.now().UTC()
	key, err := a.Key(e, h, now)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	body, err := json.Marshal(Record{
		MessageID:  e.MessageID,
		Stream:     e.Stream,
		StreamID:   e.StreamID,
		ArchivedAt: now.Format(time.RFC3339),
		Message:    e.Document,
	})
	if err != nil {
		return Result{}, fmt.Errorf("encode record %s: %w", e.MessageID, err)
	}
	if err := a.store.Put(ctx, key, body); err != nil {
		return Result{}, fmt.Errorf("put %s: %w", key, err)
	}
	return Result{Key: key}, nil
}

// Key renders the object key of e. The date is the event time: timestamp_unix,
// else the RFC3339 timestamp, else now.
func (a *Archiver) Key(e Entry, h header, now time.Time) (string, error) {
	t := eventTime(h, now)
	return render(a.key, KeyFields{
		System:    segment(h.System),
		Severity:  segment(strings.ToLower(h.Severity)),
		Date:      t.Format("2006-01-02"),
		Year:      t.Format("2006"),
		Month:     t.Format("01"),
		Day:       t.Format("02"),
		Hour:      t.Format("15"),
		MessageID: segment(e.MessageID),
		Stream:    segment(e.Stream),
	})
}

func render(tmpl *template.Template, f KeyFields) (string, error) {
	var b bytes.Buffer
	if err := tmpl.Execute(&b, f); err != nil {
		return "", fmt.Errorf("render key: %w", err)
	}
	key := strings.TrimLeft(b.String(), "/")
	if key == "" {
		return "", fmt.Errorf("render key: empty")
	}
	return key, nil
}

func eventTime(h header, now time.Time) time.Time {
	if h.TimestampUnix > 0 {
		return time.Unix(h.TimestampUnix, 0).UTC()
	}
	if t, err := time.Parse(time.RFC3339, h.Timestamp); err == nil {
		return t.UTC()
	}
	return now
}

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9._=+@-]+`)

// segment makes a value safe as one path segment of a key: no slashes, no
// "." or "..", nothing an S3 client or a Hive partition reader trips over.
func segment(v string) string {
	v = strings.Trim(unsafeChars.ReplaceAllString(v, "_"), "_.")
	if v == "" || v == "." || v == ".." {
		return "unknown"
	}
	return v
}
