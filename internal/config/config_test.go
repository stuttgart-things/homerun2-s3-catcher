package config

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	c, err := load(env(map[string]string{"S3_ENDPOINT": "minio:9000", "S3_BUCKET": "homerun"}))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]any{
		"redis":       c.Redis.Addr + ":" + c.Redis.Port,
		"streams":     strings.Join(c.Streams, ","),
		"group":       c.ConsumerGroup,
		"startID":     c.ConsumerStartID,
		"key":         c.KeyTemplate,
		"s3":          c.S3,
		"maxLag":      c.MaxLag,
		"visibility":  c.VisibilityTimeout,
		"lagInterval": c.LagInterval,
	}
	want := map[string]any{
		"redis":       "localhost:6379",
		"streams":     "messages",
		"group":       DefaultConsumerGroup,
		"startID":     "0",
		"key":         DefaultKeyTemplate,
		"s3":          S3Config{Endpoint: "minio:9000", Bucket: "homerun", Region: "us-east-1", UseSSL: true},
		"maxLag":      12 * time.Hour,
		"visibility":  time.Minute,
		"lagInterval": time.Minute,
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %v, want %v", k, got[k], w)
		}
	}
}

func TestLoadValues(t *testing.T) {
	c, err := load(env(map[string]string{
		"S3_ENDPOINT": "minio:9000", "S3_BUCKET": "b", "S3_USE_SSL": "false", "S3_INSECURE": "true",
		"REDIS_STREAMS": "messages, k8s-events,,", "SYSTEMS": "k8s-labda,flux", "MAX_LAG": "0",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.Streams, "|") != "messages|k8s-events" || strings.Join(c.Systems, "|") != "k8s-labda|flux" {
		t.Errorf("lists: %v %v", c.Streams, c.Systems)
	}
	if c.S3.UseSSL || !c.S3.Insecure || c.MaxLag != 0 {
		t.Errorf("values: %+v maxLag %v", c.S3, c.MaxLag)
	}
}

func TestLoadErrors(t *testing.T) {
	base := map[string]string{"S3_ENDPOINT": "minio:9000", "S3_BUCKET": "b"}
	for _, tc := range []struct{ key, val, want string }{
		{"S3_ENDPOINT", "", "S3_ENDPOINT is required"},
		{"S3_ENDPOINT", "https://minio:9000", "without scheme"},
		{"S3_BUCKET", "", "S3_BUCKET is required"},
		{"S3_USE_SSL", "yes please", "S3_USE_SSL"},
		{"MAX_LAG", "-1h", "MAX_LAG"},
		{"VISIBILITY_TIMEOUT", "0", "VISIBILITY_TIMEOUT"},
		{"LAG_INTERVAL", "soon", "LAG_INTERVAL"},
	} {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		m[tc.key] = tc.val
		if _, err := load(env(m)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s=%q: err = %v, want %q", tc.key, tc.val, err, tc.want)
		}
	}
}
