// Package catcher consumes homerun stream entries and archives the messages
// they reference.
package catcher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stuttgart-things/redisqueue"

	"github.com/stuttgart-things/homerun2-s3-catcher/internal/archive"
)

// Archiver archives one entry; see archive.Archiver.
type Archiver interface {
	Archive(ctx context.Context, e archive.Entry) (archive.Result, error)
}

// DocClient resolves a stream entry's Redis JSON document (JSON.GET).
type DocClient interface {
	Do(ctx context.Context, args ...any) *redis.Cmd
}

// Stats counts what the catcher did. Safe for concurrent use.
type Stats struct {
	Archived atomic.Int64 // written to S3
	Skipped  atomic.Int64 // filtered out by system
	Missing  atomic.Int64 // document already gone from Redis
	Invalid  atomic.Int64 // can never be archived
	Failed   atomic.Int64 // failed attempts, delivered again later
	lastUnix atomic.Int64
}

// LastArchived is the time of the last successful upload, zero if none.
func (s *Stats) LastArchived() time.Time {
	if u := s.lastUnix.Load(); u > 0 {
		return time.Unix(u, 0).UTC()
	}
	return time.Time{}
}

// Options configure the consumer.
type Options struct {
	Streams           []string
	Group             string
	Consumer          string // default: hostname
	StartID           string // for a new group: "0" (whole stream), "$" or an ID
	VisibilityTimeout time.Duration
	Concurrency       int
}

// Catcher consumes the streams and archives every entry. An entry is
// acknowledged once its message is archived, skipped or found unarchivable;
// a failed upload stays pending and is delivered again after the visibility
// timeout.
type Catcher struct {
	consumer *redisqueue.Consumer
	docs     DocClient
	archiver Archiver
	stats    *Stats
	// attempt bounds one JSON.GET plus upload. It is shorter than the
	// visibility timeout, so an attempt has failed before the entry is
	// reclaimed; otherwise a hanging S3 piles up parallel attempts of the
	// same entry.
	attempt time.Duration
}

// blockingTimeout bounds each XREADGROUP, and so how long Shutdown waits.
var blockingTimeout = 5 * time.Second

var startIDPattern = regexp.MustCompile(`^(\$|\d+(-\d+)?)$`)

// New wires a catcher: consumerClient drives the stream consumer, docs
// resolves the referenced Redis JSON documents.
func New(consumerClient redis.UniversalClient, docs DocClient, a Archiver, stats *Stats, o Options) (*Catcher, error) {
	if len(o.Streams) == 0 {
		return nil, fmt.Errorf("no streams configured")
	}
	if !startIDPattern.MatchString(o.StartID) {
		return nil, fmt.Errorf("invalid consumer start ID %q: use \"0\", \"$\" or a stream ID", o.StartID)
	}
	if o.Consumer == "" {
		o.Consumer, _ = os.Hostname()
	}
	if o.Concurrency < 1 {
		o.Concurrency = 1
	}

	consumer, err := redisqueue.NewConsumerWithOptions(&redisqueue.ConsumerOptions{
		Name:              o.Consumer,
		GroupName:         o.Group,
		VisibilityTimeout: o.VisibilityTimeout,
		ReclaimInterval:   o.VisibilityTimeout,
		BlockingTimeout:   blockingTimeout,
		BufferSize:        100,
		Concurrency:       o.Concurrency,
		RedisClient:       consumerClient,
	})
	if err != nil {
		return nil, fmt.Errorf("create redis consumer: %w", err)
	}

	c := &Catcher{consumer: consumer, docs: docs, archiver: a, stats: stats, attempt: attemptTimeout(o.VisibilityTimeout)}
	for _, s := range o.Streams {
		consumer.RegisterWithLastID(s, o.StartID, c.handle)
	}
	return c, nil
}

// Run consumes until Shutdown.
func (c *Catcher) Run() { c.consumer.Run() }

// Shutdown stops the consumer.
func (c *Catcher) Shutdown() { c.consumer.Shutdown() }

// Errors are the consumer's errors, failed uploads included.
func (c *Catcher) Errors() <-chan error { return c.consumer.Errors }

// maxAttempt caps the attempt timeout for long visibility timeouts.
const maxAttempt = 30 * time.Second

// attemptTimeout is half the visibility timeout, at most maxAttempt.
func attemptTimeout(visibility time.Duration) time.Duration {
	if visibility <= 0 {
		return maxAttempt
	}
	return min(visibility/2, maxAttempt)
}

// handle archives one entry. A nil return acknowledges it.
func (c *Catcher) handle(msg *redisqueue.Message) error {
	id, _ := msg.Values["messageID"].(string)
	if id == "" {
		slog.Warn("stream entry without messageID, acknowledged", "stream", msg.Stream, "id", msg.ID)
		c.stats.Invalid.Add(1)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.attempt)
	defer cancel()

	doc, err := c.docs.Do(ctx, "JSON.GET", id, ".").Text()
	if errors.Is(err, redis.Nil) {
		// Retention was faster than the archive: nothing left to archive.
		slog.Warn("message already gone from redis, acknowledged",
			"stream", msg.Stream, "id", msg.ID, "messageID", id)
		c.stats.Missing.Add(1)
		return nil
	}
	if err != nil {
		c.stats.Failed.Add(1)
		return fmt.Errorf("JSON.GET %s: %w", id, err)
	}

	res, err := c.archiver.Archive(ctx, archive.Entry{
		Stream: msg.Stream, StreamID: msg.ID, MessageID: id, Document: []byte(doc),
	})
	switch {
	case errors.Is(err, archive.ErrInvalid):
		slog.Error("message cannot be archived, acknowledged",
			"stream", msg.Stream, "id", msg.ID, "messageID", id, "error", err)
		c.stats.Invalid.Add(1)
		return nil
	case err != nil:
		c.stats.Failed.Add(1)
		return err
	case res.Skipped:
		c.stats.Skipped.Add(1)
		return nil
	}

	c.stats.Archived.Add(1)
	c.stats.lastUnix.Store(time.Now().Unix())
	slog.Debug("message archived", "stream", msg.Stream, "id", msg.ID, "key", res.Key)
	return nil
}
