// Package config loads the catcher's settings from the environment.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	homerun "github.com/stuttgart-things/homerun-library/v4"
)

// Config is everything the catcher reads from the environment.
type Config struct {
	Redis           homerun.RedisConfig
	Streams         []string
	ConsumerGroup   string
	ConsumerName    string
	ConsumerStartID string
	// VisibilityTimeout is how long an unacknowledged entry (a failed
	// upload) stays pending before it is delivered again.
	VisibilityTimeout time.Duration

	S3 S3Config

	// KeyTemplate is the Go template of the object key, see archive.Key.
	KeyTemplate string
	// Systems limits archiving to these message systems; empty archives all.
	Systems []string

	// MaxLag is the age of the oldest unarchived entry above which the
	// catcher reports itself degraded; 0 disables the check.
	MaxLag time.Duration
	// LagInterval is how often the lag is measured.
	LagInterval time.Duration

	HealthPort string
}

// S3Config addresses the bucket.
type S3Config struct {
	Endpoint     string // host[:port], without scheme
	Bucket       string
	Region       string
	AccessKey    string
	SecretKey    string
	UseSSL       bool
	Insecure     bool // skip TLS verification
	CreateBucket bool
}

// Defaults of the settings that have one.
const (
	DefaultConsumerGroup     = "homerun2-s3-catcher"
	DefaultConsumerStartID   = "0"
	DefaultKeyTemplate       = "system={{.System}}/date={{.Date}}/{{.MessageID}}.json"
	DefaultVisibilityTimeout = time.Minute
	DefaultMaxLag            = 12 * time.Hour
	DefaultLagInterval       = time.Minute
)

// Load reads the configuration from the environment.
func Load() (Config, error) {
	return load(os.Getenv)
}

func load(getenv func(string) string) (Config, error) {
	get := func(key, def string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return def
	}

	c := Config{
		Redis: homerun.RedisConfig{
			Addr:     get("REDIS_ADDR", "localhost"),
			Port:     get("REDIS_PORT", "6379"),
			Password: getenv("REDIS_PASSWORD"),
			Stream:   get("REDIS_STREAM", "messages"),
		},
		Streams:         ParseList(getenv("REDIS_STREAMS")),
		ConsumerGroup:   get("CONSUMER_GROUP", DefaultConsumerGroup),
		ConsumerName:    getenv("CONSUMER_NAME"),
		ConsumerStartID: get("CONSUMER_START_ID", DefaultConsumerStartID),
		S3: S3Config{
			Endpoint:  getenv("S3_ENDPOINT"),
			Bucket:    getenv("S3_BUCKET"),
			Region:    get("S3_REGION", "us-east-1"),
			AccessKey: getenv("S3_ACCESS_KEY"),
			SecretKey: getenv("S3_SECRET_KEY"),
		},
		KeyTemplate: get("S3_KEY_TEMPLATE", DefaultKeyTemplate),
		Systems:     ParseList(getenv("SYSTEMS")),
		HealthPort:  get("HEALTH_PORT", "8080"),
	}
	if len(c.Streams) == 0 {
		c.Streams = []string{c.Redis.Stream}
	}

	var err error
	if c.S3.UseSSL, err = parseBool("S3_USE_SSL", get("S3_USE_SSL", "true")); err != nil {
		return c, err
	}
	if c.S3.Insecure, err = parseBool("S3_INSECURE", get("S3_INSECURE", "false")); err != nil {
		return c, err
	}
	if c.S3.CreateBucket, err = parseBool("S3_CREATE_BUCKET", get("S3_CREATE_BUCKET", "false")); err != nil {
		return c, err
	}
	if c.VisibilityTimeout, err = parseDuration("VISIBILITY_TIMEOUT", getenv("VISIBILITY_TIMEOUT"), DefaultVisibilityTimeout); err != nil {
		return c, err
	}
	if c.MaxLag, err = parseDuration("MAX_LAG", getenv("MAX_LAG"), DefaultMaxLag); err != nil {
		return c, err
	}
	if c.LagInterval, err = parseDuration("LAG_INTERVAL", getenv("LAG_INTERVAL"), DefaultLagInterval); err != nil {
		return c, err
	}

	if c.S3.Endpoint == "" {
		return c, fmt.Errorf("S3_ENDPOINT is required")
	}
	if strings.Contains(c.S3.Endpoint, "://") {
		return c, fmt.Errorf("S3_ENDPOINT %q: give host[:port] without scheme, TLS is S3_USE_SSL", c.S3.Endpoint)
	}
	if c.S3.Bucket == "" {
		return c, fmt.Errorf("S3_BUCKET is required")
	}
	if c.VisibilityTimeout <= 0 {
		return c, fmt.Errorf("VISIBILITY_TIMEOUT must be positive")
	}
	if c.LagInterval <= 0 {
		return c, fmt.Errorf("LAG_INTERVAL must be positive")
	}
	return c, nil
}

// ParseList splits a comma-separated value, dropping empty items.
func ParseList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseBool(key, v string) (bool, error) {
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("invalid %s %q: use true or false", key, v)
	}
	return b, nil
}

// parseDuration parses a Go duration; empty is def, "0" is 0.
func parseDuration(key, v string, def time.Duration) (time.Duration, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("invalid %s %q: use a duration such as 30s, 5m or 12h", key, v)
	}
	return d, nil
}

// SetupLogging configures slog from LOG_FORMAT (json, text) and LOG_LEVEL.
func SetupLogging() {
	var level slog.Level
	switch strings.ToLower(homerun.GetEnv("LOG_LEVEL", "info")) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler = slog.NewJSONHandler(os.Stdout, opts)
	if strings.ToLower(homerun.GetEnv("LOG_FORMAT", "json")) == "text" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}
	slog.SetDefault(slog.New(handler))
	homerun.SetLogger(slog.Default())
}
