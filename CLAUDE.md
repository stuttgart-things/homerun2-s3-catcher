# CLAUDE.md

## Project

homerun2-s3-catcher — Go microservice that consumes homerun2 messages from Redis Streams and archives them to S3 (AWS, MinIO, RustFS) as JSON objects. Long-term copy of what homerun2-scout's retention prunes from Redis.

## Tech Stack

- **Language**: Go 1.26+
- **Consumer**: Redis Streams via `redisqueue` (consumer groups, reclaim of pending entries)
- **Library**: `homerun-library/v4` (startup wait, logging)
- **S3**: `minio-go/v7`
- **Build**: ko (`.ko.yaml`), no Dockerfile
- **CI**: Dagger modules (`dagger/main.go`), Taskfile
- **Deploy**: KCL manifests (`kcl/`), kustomize OCI, semantic-release

## Architecture

```
Redis Stream ──► catcher (JSON.GET messageID) ──► archive (key template, envelope) ──► S3
                         │
                         └── ack only after a successful upload; failures stay pending
lag monitor ── oldest unarchived entry per stream ──► /healthz (degraded above MAX_LAG)
```

## Key Paths

- `main.go` — entrypoint: config, waits for Redis and the bucket, wiring
- `internal/config/` — env configuration and validation
- `internal/archive/` — key template, record envelope, `ErrInvalid`, S3 store (`s3.go`)
- `internal/catcher/` — redisqueue consumer; `handle` decides ack (nil) vs. redelivery (error)
- `internal/lag/` — age of the oldest unarchived entry (pending or undelivered)
- `internal/handlers/` — `/healthz` with counters and lag
- `kcl/` — Deployment, ConfigMap, Secrets (Redis, S3 or existing Secret), Service

## Git Workflow

**Branch-per-issue with PR and merge.**

- `fix/<issue-number>-<short-description>`, `feat/…`, `test/…`, `chore/…`
- Conventional commits: `fix:`, `feat:`, `test:`, `chore:`, `docs:`
- Include `Closes #<issue-number>`

## Code Conventions

- No Dockerfile — use ko for image builds
- Config via environment variables, loaded once at startup
- Tests: `go test ./...` — unit tests must not require Redis or S3; `task build-test-binary` runs the binary against Redis Stack and RustFS in Dagger
- Logging: `log/slog`

## Testing

```bash
go test ./...
task lint
task build-test-binary
task build-scan-image-ko
```
