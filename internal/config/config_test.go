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
	if c.Redis.Addr != "localhost" || c.Redis.Port != "6379" || len(c.Streams) != 1 || c.Streams[0] != "messages" {
		t.Errorf("redis = %+v streams %v", c.Redis, c.Streams)
	}
	if c.ConsumerGroup != DefaultConsumerGroup || c.ConsumerStartID != "0" || c.KeyTemplate != DefaultKeyTemplate {
		t.Errorf("consumer/key defaults: %+v", c)
	}
	if !c.S3.UseSSL || c.S3.Insecure || c.S3.CreateBucket || c.S3.Region != "us-east-1" {
		t.Errorf("s3 defaults: %+v", c.S3)
	}
	if c.MaxLag != 12*time.Hour || c.VisibilityTimeout != time.Minute || c.LagInterval != time.Minute {
		t.Errorf("durations: %v %v %v", c.MaxLag, c.VisibilityTimeout, c.LagInterval)
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
