// homerun2-s3-catcher archives homerun2 messages from Redis Streams to S3.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	homerun "github.com/stuttgart-things/homerun-library/v4"

	"github.com/stuttgart-things/homerun2-s3-catcher/internal/archive"
	"github.com/stuttgart-things/homerun2-s3-catcher/internal/catcher"
	"github.com/stuttgart-things/homerun2-s3-catcher/internal/config"
	"github.com/stuttgart-things/homerun2-s3-catcher/internal/handlers"
	"github.com/stuttgart-things/homerun2-s3-catcher/internal/inventory"
	"github.com/stuttgart-things/homerun2-s3-catcher/internal/lag"
	"github.com/stuttgart-things/homerun2-s3-catcher/internal/metrics"
	"github.com/stuttgart-things/homerun2-s3-catcher/internal/ui"
)

// Build-time variables set via ldflags.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

// bucketTimeout bounds how long startup waits for the S3 endpoint; retries
// back off up to maxBucketBackoff.
const (
	bucketTimeout    = 2 * time.Minute
	maxBucketBackoff = 15 * time.Second
	// rebuildTimeout bounds rebuilding the snapshot gauges at startup.
	rebuildTimeout = 5 * time.Minute
)

func main() {
	config.SetupLogging()
	slog.Info("starting homerun2-s3-catcher",
		"version", version, "commit", commit, "date", date, "go", runtime.Version())

	cfg, err := config.Load()
	if err != nil {
		fail("invalid configuration", err)
	}

	// Canceled on SIGINT/SIGTERM: ends the startup waits, then the catcher.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := archive.NewS3Store(s3Options(cfg.S3))
	if err != nil {
		fail("invalid S3 configuration", err)
	}
	archiver, err := archive.New(store, cfg.KeyTemplate, cfg.Systems)
	if err != nil {
		fail("invalid configuration", err)
	}

	waitForRedis(ctx, cfg.Redis)
	waitForBucket(ctx, store, cfg.S3.CreateBucket)

	opts := &redis.Options{Addr: cfg.Redis.Addr + ":" + cfg.Redis.Port, Password: cfg.Redis.Password}
	consumerClient, docsClient := redis.NewClient(opts), redis.NewClient(opts)
	defer func() { _ = docsClient.Close() }()

	stats := &catcher.Stats{}
	monitor := lag.New(docsClient, cfg.ConsumerGroup, cfg.Streams, cfg.MaxLag)
	m := metrics.New(monitor)

	// The UI's view of the buckets: the archive, and the analyses if a
	// bucket for them is configured.
	var analysisStore *archive.S3Store
	if cfg.Analysis.Bucket != "" {
		if analysisStore, err = archive.NewS3Store(s3Options(cfg.Analysis)); err != nil {
			fail("invalid analysis S3 configuration", err)
		}
	}
	var analysisLister inventory.Lister
	var analysisGetter ui.Getter
	if analysisStore != nil {
		analysisLister, analysisGetter = analysisStore, analysisStore
	}
	inv := inventory.New(store, analysisLister, cfg.InventoryDays)
	watchAnalyses(inv, m, analysisStore)
	web := &ui.UI{
		BasePath: cfg.BasePath, Bucket: cfg.S3.Bucket, Info: ui.BuildInfo{Version: version, Commit: commit, Date: date},
		Stats: stats, Lag: monitor, Inv: inv, Archive: store, Analysis: analysisGetter, Started: time.Now(),
	}

	c, err := catcher.New(consumerClient, docsClient, archiver, stats, catcher.Options{
		Streams:           cfg.Streams,
		Group:             cfg.ConsumerGroup,
		Consumer:          cfg.ConsumerName,
		StartID:           cfg.ConsumerStartID,
		VisibilityTimeout: cfg.VisibilityTimeout,
		Concurrency:       4,
		Observer:          m,
	})
	if err != nil {
		fail("failed to create catcher", err)
	}

	go serveHTTP(cfg.HealthPort, handlers.Mount(cfg.BasePath, web.Handler(), handlers.NewHealthHandler(
		handlers.BuildInfo{Version: version, Commit: commit, Date: date}, stats, monitor), m.Handler()), cfg.BasePath)
	go rebuildMetrics(ctx, m, store)
	go monitor.Run(ctx, cfg.LagInterval)
	go inv.Run(ctx, cfg.InventoryInterval)
	go func() {
		for err := range c.Errors() {
			slog.Error("consumer error", "error", err)
		}
	}()
	go func() {
		<-ctx.Done()
		slog.Info("shutting down catcher")
		c.Shutdown()
	}()

	slog.Info("catcher running",
		"redis", opts.Addr, "streams", cfg.Streams,
		"consumer_group", cfg.ConsumerGroup, "consumer_start_id", cfg.ConsumerStartID,
		"s3_endpoint", cfg.S3.Endpoint, "bucket", cfg.S3.Bucket, "key_template", cfg.KeyTemplate,
		"systems", cfg.Systems, "visibility_timeout", cfg.VisibilityTimeout.String(),
		"max_lag", cfg.MaxLag.String())
	c.Run()
	slog.Info("catcher exited gracefully")
}

// watchAnalyses keeps the analysis metrics on the newest report: they
// follow the inventory's listing of the analysis bucket (INVENTORY_INTERVAL).
func watchAnalyses(inv *inventory.Inventory, m *metrics.Metrics, analysisStore *archive.S3Store) {
	if analysisStore == nil {
		return
	}
	w := &metrics.AnalysisWatcher{Metrics: m, Getter: analysisStore}
	inv.OnRefresh = func(ctx context.Context, s inventory.Snapshot) { w.Update(ctx, s.Analyses) }
}

// rebuildMetrics sets the snapshot gauges from the archive, so a restart
// does not zero the dashboards; live snapshots that arrive meanwhile win.
func rebuildMetrics(ctx context.Context, m *metrics.Metrics, store *archive.S3Store) {
	rctx, cancel := context.WithTimeout(ctx, rebuildTimeout)
	defer cancel()
	m.Rebuild(rctx, store, metrics.DefaultRebuildOptions)
}

func fail(msg string, err error) {
	slog.Error(msg, "error", err)
	os.Exit(1)
}

// waitForRedis blocks until Redis answers, or exits after
// REDIS_STARTUP_TIMEOUT; a shutdown signal ends the wait with exit code 0.
func waitForRedis(ctx context.Context, rc homerun.RedisConfig) {
	timeout, err := homerun.LoadRedisStartupTimeout()
	if err != nil {
		fail("invalid configuration", err)
	}
	if err := homerun.WaitForRedisContext(ctx, rc, timeout); err != nil {
		if ctx.Err() != nil {
			slog.Info("shutdown requested while waiting for redis")
			os.Exit(0)
		}
		slog.Error("redis not reachable", "error", err, "addr", rc.Addr, "port", rc.Port,
			"startup_timeout", timeout.String())
		os.Exit(1)
	}
}

// waitForBucket retries the bucket check until it succeeds or bucketTimeout
// passes: an S3 endpoint that is still starting must not crash the catcher.
func waitForBucket(ctx context.Context, store *archive.S3Store, create bool) {
	deadline := time.Now().Add(bucketTimeout)
	backoff := time.Second
	for {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := store.EnsureBucket(cctx, create)
		cancel()
		if err == nil {
			return
		}
		if ctx.Err() != nil {
			slog.Info("shutdown requested while waiting for S3")
			os.Exit(0)
		}
		if time.Now().After(deadline) {
			fail("S3 bucket not usable", err)
		}
		slog.Warn("S3 bucket not usable yet, retrying", "error", err, "retry_in", backoff.String())
		select {
		case <-ctx.Done():
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBucketBackoff)
	}
}

func s3Options(c config.S3Config) archive.S3Options {
	return archive.S3Options{
		Endpoint: c.Endpoint, Bucket: c.Bucket, Region: c.Region,
		AccessKey: c.AccessKey, SecretKey: c.SecretKey, UseSSL: c.UseSSL, Insecure: c.Insecure,
	}
}

func serveHTTP(port string, h http.Handler, basePath string) {
	srv := &http.Server{Addr: ":" + port, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	slog.Info("http server starting", "port", port, "base_path", basePath)
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("http server error", "error", err)
	}
}
