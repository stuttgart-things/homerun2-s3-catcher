// Dagger CI module for homerun2-s3-catcher
//
// Provides build, lint, test, image build, and security scanning functions.

package main

import (
	"context"
	"dagger/dagger/internal/dagger"
	"fmt"
)

type Dagger struct{}

// Lint runs golangci-lint on the source code
func (m *Dagger) Lint(
	ctx context.Context,
	src *dagger.Directory,
	// +optional
	// +default="500s"
	timeout string,
) *dagger.Container {
	return dag.Go().Lint(src, dagger.GoLintOpts{
		Timeout: timeout,
	})
}

// Build compiles the Go binary
func (m *Dagger) Build(
	ctx context.Context,
	src *dagger.Directory,
	// +optional
	// +default="main"
	binName string,
	// +optional
	// +default=""
	ldflags string,
	// +optional
	// +default="1.26.11"
	goVersion string,
	// +optional
	// +default="linux"
	os string,
	// +optional
	// +default="amd64"
	arch string,
) *dagger.Directory {
	return dag.Go().BuildBinary(src, dagger.GoBuildBinaryOpts{
		GoVersion:  goVersion,
		Os:         os,
		Arch:       arch,
		BinName:    binName,
		Ldflags:    ldflags,
		GoMainFile: "main.go",
	})
}

// BuildImage builds a container image using ko and optionally pushes it
func (m *Dagger) BuildImage(
	ctx context.Context,
	src *dagger.Directory,
	// +optional
	// +default="ko.local/homerun2-s3-catcher"
	repo string,
	// +optional
	// +default="false"
	push string,
) (string, error) {
	return dag.Go().KoBuild(ctx, src, dagger.GoKoBuildOpts{
		Repo: repo,
		Push: push,
	})
}

// ScanImage scans a container image for vulnerabilities using Trivy
func (m *Dagger) ScanImage(
	ctx context.Context,
	imageRef string,
	// +optional
	// +default="HIGH,CRITICAL"
	severity string,
) *dagger.File {
	return dag.Trivy().ScanImage(imageRef, dagger.TrivyScanImageOpts{
		Severity: severity,
	})
}

// BuildAndTestBinary runs the unit tests, builds the binary and runs it
// against Redis Stack and an S3 compatible store (RustFS): a pitched message
// with a payload must be archived, a stream entry whose document is gone must
// be acknowledged as missing.
func (m *Dagger) BuildAndTestBinary(
	ctx context.Context,
	source *dagger.Directory,
	// +optional
	// +default="1.26.11"
	goVersion string,
	// +optional
	// +default="linux"
	os string,
	// +optional
	// +default="amd64"
	arch string,
	// +optional
	// +default="main.go"
	goMainFile string,
	// +optional
	// +default="main"
	binName string,
	// +optional
	// +default=""
	ldflags string,
	// +optional
	// +default="."
	packageName string,
) (*dagger.File, error) {

	// Unit tests first: they need neither Redis nor S3.
	if _, err := dag.Container().
		From("golang:"+goVersion).
		WithDirectory("/src", source).
		WithWorkdir("/src").
		WithExec([]string{"go", "test", "./..."}).
		Sync(ctx); err != nil {
		return nil, fmt.Errorf("unit tests failed: %w", err)
	}

	binDir := dag.Go().BuildBinary(
		source,
		dagger.GoBuildBinaryOpts{
			GoVersion:   goVersion,
			Os:          os,
			Arch:        arch,
			GoMainFile:  goMainFile,
			BinName:     binName,
			Ldflags:     ldflags,
			PackageName: packageName,
		})

	redisService := dag.Homerun().RedisService(dagger.HomerunRedisServiceOpts{
		Version:  "7.2.0-v18",
		Password: "",
	})

	s3Service := dag.Container().
		From("rustfs/rustfs:1.0.4").
		WithEnvVariable("RUSTFS_ACCESS_KEY", "testuser").
		WithEnvVariable("RUSTFS_SECRET_KEY", "testpassword123").
		WithExposedPort(9000).
		AsService(dagger.ContainerAsServiceOpts{UseEntrypoint: true})

	testCmd := fmt.Sprintf(`
exec > /app/test-output.log 2>&1
set -e
apk add --no-cache redis curl > /dev/null 2>&1

counter() { curl -s localhost:8080/healthz | sed -n "s/.*\"$1\":\([0-9]*\).*/\1/p"; }

echo "=== Pitching a message with payload and a dangling entry ==="
redis-cli -h redis JSON.SET it-k8s . '{"title":"Pod default/web-0: 1 finding","severity":"warning","system":"k8s-it","timestamp":"2026-11-13T10:00:00Z","payload":{"spec":{"error":[{"text":"back-off"}]}}}'
redis-cli -h redis XADD messages '*' messageID it-k8s
redis-cli -h redis XADD messages '*' messageID it-gone

echo "=== Starting s3-catcher ==="
./%s &
BIN_PID=$!

for i in $(seq 1 60); do
  if [ "$(counter archived)" = "1" ] && [ "$(counter missing)" = "1" ]; then
    echo "archived=1 missing=1"
    curl -s localhost:8080/healthz
    echo ""
    echo "=== All tests passed! ==="
    kill $BIN_PID 2>/dev/null || true
    exit 0
  fi
  if ! kill -0 $BIN_PID 2>/dev/null; then
    echo "s3-catcher exited"
    exit 1
  fi
  sleep 1
done

echo "=== Timeout: message not archived ==="
curl -s localhost:8080/healthz || true
exit 1
`, binName)

	result := dag.Container().
		From("alpine:3.22").
		WithDirectory("/app", binDir).
		WithWorkdir("/app").
		WithServiceBinding("redis", redisService).
		WithServiceBinding("s3", s3Service).
		WithEnvVariable("REDIS_ADDR", "redis").
		WithEnvVariable("REDIS_PORT", "6379").
		WithEnvVariable("REDIS_STREAM", "messages").
		WithEnvVariable("S3_ENDPOINT", "s3:9000").
		WithEnvVariable("S3_BUCKET", "homerun-archive").
		WithEnvVariable("S3_ACCESS_KEY", "testuser").
		WithEnvVariable("S3_SECRET_KEY", "testpassword123").
		WithEnvVariable("S3_USE_SSL", "false").
		WithEnvVariable("S3_CREATE_BUCKET", "true").
		WithEnvVariable("LOG_FORMAT", "text").
		WithExec([]string{"sh", "-c", testCmd}, dagger.ContainerWithExecOpts{})

	_, err := result.Sync(ctx)
	if err != nil {
		testLog := result.File("/app/test-output.log")
		return testLog, fmt.Errorf("tests failed - check test-output.log for details: %w", err)
	}

	return result.File("/app/test-output.log"), nil
}
