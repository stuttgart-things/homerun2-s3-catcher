# homerun2-s3-catcher

Archives homerun2 messages from Redis Streams to S3 (AWS, MinIO, RustFS, any S3 compatible store) as JSON. Redis keeps messages only as long as homerun2-scout's retention allows (`SCOUT_RETENTION_TTL`, default 48h). This catcher is the long-term copy.

```
pitchers → Redis (JSON document + stream entry messageID) → s3-catcher → S3
```

The first use case is K8sGPT findings: homerun2-k8s-pitcher pitches each K8sGPT `Result` with the CR as `payload`, and the archive keeps it as nested JSON.

## How it works

- **Consumer group:** `homerun2-s3-catcher` reads the stream entries. For each one it calls `JSON.GET <messageID>`, then `PutObject`.
- **At least once:** an entry is acknowledged only after a successful upload. A failed upload (S3 down, timeout) stays pending and is delivered again after `VISIBILITY_TIMEOUT`. A single attempt is bounded to half of that, so a hanging S3 does not pile up parallel attempts.
- **Idempotent:** the object key contains the messageID, so a repeated delivery overwrites the same object.
- **Backlog:** a new group starts at `0` (`CONSUMER_START_ID`), so everything still in the stream is archived. An existing group keeps its position across restarts.
- **Acknowledged without an upload:** entries whose document is already gone (retention was faster; counted as `missing`), entries that can never be archived (`invalid`), and systems filtered out by `SYSTEMS` (`skipped`).
- **Lag against retention:** every `LAG_INTERVAL` the age of the oldest unarchived entry is measured per stream, whether pending or not yet delivered. Above `MAX_LAG`, `/healthz` reports `degraded` and a warning is logged. Keep `MAX_LAG` well below scout's retention, or messages get pruned before they are archived.

### Object layout

Key (default, Hive-style partitions):

```
system=<system>/date=<event date>/<messageID>.json
```

Body:

```json
{
  "messageID": "1f0c…-k8s-labda",
  "stream": "messages",
  "streamID": "1794574784007-0",
  "archivedAt": "2026-11-13T13:00:37Z",
  "message": {
    "title": "Pod default/web-0: 1 finding",
    "severity": "warning",
    "system": "k8s-labda",
    "timestamp": "2026-11-12T23:59:00Z",
    "payload": { "kind": "Result", "spec": { "error": [ { "text": "Back-off restarting failed container" } ] } }
  }
}
```

`message` is the Redis JSON document as stored, unknown fields included. The event date comes from `timestamp_unix`, else `timestamp`, else the archive time.

Example query with DuckDB:

```sql
SELECT system, date, message.title, message.payload.spec.error
FROM read_json('s3://homerun-archive/**/*.json', hive_partitioning = true)
WHERE system = 'k8s-labda' AND date >= '2026-11-01';
```

## Configuration

| Variable | Default | |
|---|---|---|
| `REDIS_ADDR` / `REDIS_PORT` / `REDIS_PASSWORD` | `localhost` / `6379` / – | |
| `REDIS_STREAMS` | `REDIS_STREAM`, else `messages` | Comma-separated streams |
| `REDIS_STARTUP_TIMEOUT` | `120s` | How long startup waits for Redis |
| `CONSUMER_GROUP` | `homerun2-s3-catcher` | |
| `CONSUMER_NAME` | hostname | One consumer per pod |
| `CONSUMER_START_ID` | `0` | Start of a **new** group: `0` = whole stream, `$` = only new entries |
| `VISIBILITY_TIMEOUT` | `1m` | After this long a failed (pending) entry is delivered again |
| `S3_ENDPOINT` | **required** | `host[:port]`, without scheme |
| `S3_BUCKET` | **required** | |
| `S3_REGION` | `us-east-1` | |
| `S3_ACCESS_KEY` / `S3_SECRET_KEY` | – | |
| `S3_USE_SSL` | `true` | |
| `S3_INSECURE` | `false` | Skip TLS verification (self-signed lab certificates) |
| `S3_CREATE_BUCKET` | `false` | Create the bucket if it is missing |
| `S3_KEY_TEMPLATE` | `system={{.System}}/date={{.Date}}/{{.MessageID}}.json` | Go template. Fields: `System`, `Severity`, `Date`, `Year`, `Month`, `Day`, `Hour`, `MessageID`, `Stream`. Values are sanitized to one path segment each. Keep `MessageID` in the key, it makes uploads idempotent. |
| `SYSTEMS` | *(all)* | Comma-separated systems to archive |
| `MAX_LAG` | `12h` | `0` disables the check |
| `LAG_INTERVAL` | `1m` | |
| `HEALTH_PORT` | `8080` | UI, `/healthz`, `/health` and `/metrics` |
| `BASE_PATH` | *(root)* | UI prefix behind sthings-portal, e.g. `/archive`; `/health(z)` and `/metrics` stay at the root |
| `ANALYSIS_BUCKET` | *(none)* | Bucket of the cluster-analysis reports (`analysis/…md/json`) for the Analyses tab and the `homerun_analysis_*` metrics |
| `ANALYSIS_S3_ACCESS_KEY` / `ANALYSIS_S3_SECRET_KEY` | the archive's | Credentials for `ANALYSIS_BUCKET` |
| `INVENTORY_INTERVAL` | `5m` | How often the UI lists the bucket (and the analysis metrics look for a newer report) |
| `INVENTORY_DAYS` | `14` | Days in the Uploads table (1–90) |
| `LOG_FORMAT` / `LOG_LEVEL` | `json` / `info` | |

Startup waits for Redis and up to 2 minutes for the bucket, then exits. `/healthz` returns status, counters (`archived`, `skipped`, `missing`, `invalid`, `failed`, `lastArchived`) and the lag per stream. `degraded` is still HTTP 200, so probes do not restart a catcher that is merely behind; alert on the status instead.

## UI

Read-only, served on `HEALTH_PORT` (under `BASE_PATH`), htmx and the homerun2 theme like the other catchers; inside sthings-portal it switches to the compact header and the portal's tokens (#7).

| Tab | Shows | From |
|---|---|---|
| **Status** | health, counters, lag per stream against `MAX_LAG`, the last 50 uploads | live, since the start |
| **Uploads** | objects (and bytes on hover) per system and day, the last `INVENTORY_DAYS` days, totals | bucket listing every `INVENTORY_INTERVAL` |
| **Objects** | the latest 50 objects; click shows the JSON | bucket listing |
| **Analyses** | the reports under `analysis/` in `ANALYSIS_BUCKET`, Markdown rendered (raw HTML dropped) or JSON | analysis bucket |

For homerun2-portal's overview it serves `/ui/manifest.json` with one fragment, `/ui/fragment/status` (status, archived, last upload, max lag).

## Metrics and the Fleet health dashboard

`/metrics` (Prometheus, always at the root, whatever `BASE_PATH`) turns the archive into trends (#9). The JSON in S3 stays the source of truth; the gauges are a view of the **latest snapshot per system**.

| Metric | Labels | |
|---|---|---|
| `homerun_findings` | `system`, `source`, `target`, `kind`, `severity` | Open findings in the system's latest snapshot |
| `homerun_snapshot_timestamp_seconds` | `system`, `source`, `target` | Time of that snapshot; `time() - …` is its age |
| `homerun_snapshot_open_findings` | `system`, `source`, `target` | Total open findings of that snapshot, 0 when clean |
| `homerun_snapshot_truncated_findings` | `system`, `source`, `target` | Findings the source left out (`truncated`, payload limit) |
| `homerun_archived_messages_total` | `stream`, `system` | Uploads |
| `homerun_archive_errors_total` | `stream` | Failed attempts (JSON.GET or upload), delivered again |
| `homerun_unarchived_messages_total` | `stream`, `reason` | Acknowledged without an upload: `skipped`, `missing`, `invalid` |
| `homerun_consumer_lag_seconds` | `stream` | Age of the oldest unarchived entry (the lag monitor's last measurement) |
| `homerun_consumer_pending` | `stream` | Delivered, not acknowledged |
| `homerun_analysis_open_findings` / `_new_findings` / `_resolved_findings` | – | `stats` of the latest report in `ANALYSIS_BUCKET` |
| `homerun_analysis_severity` | `severity` | 1 for the latest report's severity, 0 for the others |
| `homerun_analysis_timestamp_seconds` | – | `generatedAt` of that report |

Plus the Go and process collectors.

**Snapshots.** Only messages whose `tags` (comma-separated) contain `snapshot` update the findings gauges, in two payload shapes:

- `findings/v1` (`{"format":"findings/v1","source":…,"target":…,"items":[{"kind","severity",…}]}`): counted per `kind` and `severity` (`info|warning|error|critical`, anything else `unknown`). Without `source`/`target` they are split from the system name `<source>-<target>`.
- legacy K8sGPT (`{"count":n,"items":[{"fingerprint","object":<Result CR>}]}`): `kind` = `object.spec.kind`, severity `warning`, source `k8sgpt`, target = the system without `k8sgpt-`.

Other formats (`inventory/v1`) and finding events (no `snapshot` tag) are archived but leave the gauges alone. A new snapshot replaces all series of its system, so resolved kinds disappear; an empty `items` leaves no `homerun_findings` series, but `homerun_snapshot_open_findings` 0 and the timestamp. An older snapshot than the one in the gauges (redelivery, backlog) is ignored.

**Restart.** At startup the gauges are rebuilt from the archive in the background: per `system=` prefix the newest object that is a snapshot, searched in the newest 2 `date=` partitions, newest object first, at most 25 GETs per system. This assumes the default key layout (`system=…/date=…/`).

**Analysis.** The analysis gauges follow the inventory's listing of `ANALYSIS_BUCKET`: when a newer `analysis/…/<runId>.json` appears, it is read once.

**Dashboard.** [`dashboards/fleet-health.json`](dashboards/fleet-health.json) ("Fleet health", uid `homerun-fleet-health`) with a `$datasource` Prometheus variable, a `system` filter and `ui_url` (the s3-catcher UI, from `${UI_URL:=/archive}`): analysis severity/open/new/resolved/age, open findings per system over time by severity, a table of systems with snapshot age (red over 3h) and open findings, archived messages and errors, consumer lag, analysis new vs. resolved, and links to the UI. As a Grafana sidecar ConfigMap (label `grafana_dashboard: "1"`):

```bash
kubectl apply -n <grafana-namespace> -k dashboards/   # or a flux Kustomization, path ./dashboards, targetNamespace
```

`ui_url` per cluster (#13): a Flux Kustomization sets it with
`postBuild.substitute.UI_URL: https://portal.example.com/archive`. Without
Flux the variable keeps the literal `${UI_URL:=/archive}` — replace it before
applying. Grafana variables in the JSON are written `$name` (not `${name}`),
so Flux's substitution leaves them alone; `dashboards/dashboard_test.go`
checks that.

## Deployment

KCL manifests in `kcl/`, released as a kustomize OCI base (`ghcr.io/stuttgart-things/homerun2-s3-catcher-kustomize`). S3 credentials can be inline (`config.s3AccessKey`/`config.s3SecretKey`, rendered into `<name>-s3`) or come from an existing Secret with the keys `accessKey` and `secretKey` (`config.s3ExistingSecret`).

A ServiceMonitor (prometheus-operator) is rendered with `config.serviceMonitorEnabled: true`; `config.serviceMonitorInterval` (default `60s`) and `config.serviceMonitorLabels` (e.g. `{"release": "kube-prometheus-stack"}` for the Prometheus' selector) tune it.

```bash
task render-manifests-quick
```

## Development

```bash
go test ./...          # unit tests, no Redis or S3 needed
task build-test-binary # Dagger: unit tests + binary against Redis Stack and RustFS
task lint
task build-scan-image-ko
```
