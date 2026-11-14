package metrics

import (
	"encoding/json"
	"strings"
	"time"
)

// FormatFindingsV1 is the payload format of a findings snapshot
// (stuttgart-things/stuttgart-things#4556, "findings/v1").
const FormatFindingsV1 = "findings/v1"

// Legacy K8sGPT snapshots have no format: their source is k8sgpt, every
// finding a warning.
const (
	legacySource   = "k8sgpt"
	legacySeverity = "warning"
	unknown        = "unknown"
)

// Series is one kind and severity of a snapshot.
type Series struct {
	Kind     string
	Severity string
}

// Snapshot is the full current state of one system: its open findings
// counted per kind and severity.
type Snapshot struct {
	System    string
	Source    string
	Target    string
	At        time.Time
	Counts    map[Series]int
	Truncated int // findings the source left out (payload size)
}

// Open is the number of open findings in the snapshot.
func (s Snapshot) Open() int {
	n := 0
	for _, c := range s.Counts {
		n += c
	}
	return n
}

// message is the part of a homerun message (the Redis JSON document) the
// snapshot needs.
type message struct {
	System        string          `json:"system"`
	Tags          string          `json:"tags"`
	Timestamp     string          `json:"timestamp"`
	TimestampUnix int64           `json:"timestamp_unix"`
	Payload       json.RawMessage `json:"payload"`
}

type payload struct {
	Format    string          `json:"format"`
	Source    string          `json:"source"`
	Target    string          `json:"target"`
	Items     json.RawMessage `json:"items"`
	Truncated int             `json:"truncated"`
}

// item covers both shapes: findings/v1 (kind, severity) and the legacy
// K8sGPT item ({"fingerprint", "object": <Result CR>}).
type item struct {
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	Object   *struct {
		Spec struct {
			Kind string `json:"kind"`
		} `json:"spec"`
	} `json:"object"`
}

// ParseMessage returns the findings snapshot in a homerun message, false
// for anything else: a message without the snapshot tag, a finding event,
// another payload format (inventory/v1). now is the time of a message
// without a timestamp.
func ParseMessage(doc []byte, now time.Time) (Snapshot, bool) {
	var m message
	if err := json.Unmarshal(doc, &m); err != nil || !hasTag(m.Tags, "snapshot") || m.System == "" {
		return Snapshot{}, false
	}
	p, ok := decodePayload(m.Payload)
	if !ok {
		return Snapshot{}, false
	}

	s := Snapshot{System: m.System, At: messageTime(m, now), Counts: map[Series]int{}, Truncated: p.Truncated}
	var items []item
	if len(p.Items) > 0 && string(p.Items) != "null" {
		if err := json.Unmarshal(p.Items, &items); err != nil {
			return Snapshot{}, false
		}
	}

	switch {
	case p.Format == FormatFindingsV1:
		s.Source, s.Target = v1Labels(p, m.System)
		for _, it := range items {
			s.Counts[Series{Kind: or(it.Kind, unknown), Severity: severity(it.Severity)}]++
		}
	case p.Format == "" && p.Items != nil:
		// Legacy K8sGPT snapshot: {"count": n, "items": [...]}; without the
		// items key it is some other payload.
		s.Source = legacySource
		s.Target = strings.TrimPrefix(m.System, legacySource+"-")
		for _, it := range items {
			kind := ""
			if it.Object != nil {
				kind = it.Object.Spec.Kind
			}
			s.Counts[Series{Kind: or(kind, unknown), Severity: legacySeverity}]++
		}
	default:
		return Snapshot{}, false
	}
	return s, true
}

// v1Labels are the payload's source and target, else split from the
// system name ("<source>-<target>").
func v1Labels(p payload, system string) (source, target string) {
	src, tgt := splitSystem(system)
	return or(p.Source, src), or(p.Target, tgt)
}

// ParseRecord returns the findings snapshot in an archived object (the
// envelope around the message, see archive.Record).
func ParseRecord(body []byte) (Snapshot, bool) {
	var r struct {
		ArchivedAt string          `json:"archivedAt"`
		Message    json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(body, &r); err != nil || len(r.Message) == 0 {
		return Snapshot{}, false
	}
	at, err := time.Parse(time.RFC3339, r.ArchivedAt)
	if err != nil {
		at = time.Time{}
	}
	return ParseMessage(r.Message, at)
}

// decodePayload accepts the payload as an object or as a JSON string
// holding one.
func decodePayload(raw json.RawMessage) (payload, bool) {
	var p payload
	if len(raw) == 0 {
		return p, false
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return p, false
		}
		raw = json.RawMessage(s)
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, false
	}
	return p, true
}

func messageTime(m message, now time.Time) time.Time {
	if m.TimestampUnix > 0 {
		return time.Unix(m.TimestampUnix, 0).UTC()
	}
	if t, err := time.Parse(time.RFC3339, m.Timestamp); err == nil {
		return t.UTC()
	}
	return now.UTC()
}

func hasTag(tags, tag string) bool {
	for t := range strings.SplitSeq(tags, ",") {
		if strings.TrimSpace(t) == tag {
			return true
		}
	}
	return false
}

// splitSystem splits "<source>-<target>" at the first dash.
func splitSystem(system string) (source, target string) {
	if src, tgt, ok := strings.Cut(system, "-"); ok && src != "" && tgt != "" {
		return src, tgt
	}
	return unknown, system
}

var severities = map[string]bool{"info": true, "warning": true, "error": true, "critical": true}

func severity(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if severities[s] {
		return s
	}
	return unknown
}

func or(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
