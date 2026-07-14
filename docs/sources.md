# Sources — tenant infrastructure-log ingestion

Tenants ship infrastructure logs (hosts, containers, Kubernetes, cloud) into
their existing **projects** via standard collectors, then view and filter them
alongside application logs using the App/Infra `source_type` toggle.

A **source** is an ingestion endpoint scoped to one project. Creating a source
mints a long-lived, source-scoped ingest key. Collectors authenticate with that
key; the ingested logs land in the source's project stamped `source_type=infra`.

> Scope note: this is the first implementation pass. The **syslog-over-TLS
> listener is not built yet** — see [Deferred](#deferred) below. Everything else
> (management API, HTTP-bulk + OTLP listeners, quotas, sampling, redaction,
> retention) is implemented.

## Ingest key format

```
ls_src_live_<64 hex chars>      e.g. ls_src_live_9f1c…  (256-bit random)
```

- The **raw key is returned exactly once**, at creation (and on rotation). It is
  never stored or retrievable afterward.
- Only a SHA-256 `key_hash` (unique-indexed, constant-time compared on auth) and
  a display `key_prefix` (`ls_src_live_` + first 6 hex) are persisted.
- Present it as a bearer token: `Authorization: Bearer ls_src_live_…`.

## Management API (user JWT, org-admin only)

All under `https://<host>/v1`. Authenticated with the normal RSA-JWT bearer and
gated to org admins / super admins.

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/orgs/{org_id}/sources` | Create a source, return the one-time `ingestKey`. |
| `GET` | `/orgs/{org_id}/sources` | List sources (no keys). |
| `DELETE` | `/orgs/{org_id}/sources/{source_id}` | Soft-disable (historical logs keep their `source_id`). |
| `POST` | `/orgs/{org_id}/sources/{source_id}/rotate-key` | Mint a new key, invalidate the old. |

**Create body**: `{ "kind": "otel"|"syslog"|"fluentbit"|"vector"|"k8s"|"http", "name": "...", "projectId": "..." }`
**Create 201**: `{ id, kind, name, projectId, isActive, createdAt, ingestKey, keyPrefix }`

Errors: `403` (viewer/non-admin), `404` (project not in org), `409` (duplicate
name in org), `400` (invalid kind — the service's error set has no 422).

## Ingestion API (source key)

| Method | Path | Body |
|---|---|---|
| `POST` | `/v1/ingest/bulk` | NDJSON (`application/x-ndjson`) or a JSON array / single object. |
| `POST` | `/v1/ingest/otlp` | OTLP/HTTP logs — protobuf (`application/x-protobuf`) or JSON. |

Both return `202 Accepted` with `{ accepted, rejected, errors?: [{index, error}] }`.
Partial success is allowed (per-record rejection). The target project is always
taken from the source — a key can only ever write into its own tenant.

**Bulk record fields** (only `message` is required):
`level, message, ts, source, tags[], host, container, pod, namespace, cluster,
unit, facility, region, cloudResourceId, attributes{}`. Missing `ts` → receive
time; missing/invalid `level` → `INFO`.

**OTLP mapping**: `body`→message, `SeverityNumber`→level (table below),
`TimeUnixNano`→ts, resource+log attributes→`attributes` and infra columns
(`service.name`→unit, `k8s.pod.name`→pod, `k8s.namespace.name`→namespace,
`k8s.cluster.name`→cluster, `k8s.container.name`/`container.name`→container,
`host.name`→host, `cloud.region`→region, `cloud.resource.id`→cloudResourceId),
`trace_id`/`span_id`→`attributes` (hex). OTLP gRPC is a follow-up.

### Normalization pipeline (shared by all listeners)

1. **Severity → level** (see table); original preserved in `attributes._severity`.
2. **Format sniff**: a JSON or logfmt `message` has its fields lifted into `attributes`.
3. **Timestamp**: normalized to UTC; absurd-future timestamps clamp to receive time; backfill is allowed.
4. **Redaction** (default-on, pre-persistence): emails, IPv4, credit-card-like
   runs, bearer tokens, `ls_*_live_` keys, and `key=secret` pairs are masked.

### Quotas, rate limits, sampling

- **Per-source token bucket** (`rate_limit_per_sec` / `rate_limit_burst`,
  defaults 500/1000). Exceed → `429` + `Retry-After`; the agent buffers & retries.
- **Per-org daily quota** from the plan's `infra_daily_event_quota` (free 1e6,
  pro 5e7, ent unlimited). Over quota → `429` + a `source.quota_exceeded` log signal.
- **Sampling** (`sample_debug_info`, default 1.0): WARN/ERROR always kept;
  DEBUG/INFO kept at that rate. Dropped counts are recorded.
- **Usage counters** (`ingest_usage`): per-source/day event + byte + dropped
  counts — the feed the billing system consumes.

## Severity mapping

| Syslog severity (0–7) | Level | | OTel SeverityNumber (1–24) | Level |
|---|---|---|---|---|
| 0–3 | `ERROR` | | 1–8 | `DEBUG` |
| 4 | `WARN` | | 9–12 | `INFO` |
| 5–6 | `INFO` | | 13–16 | `WARN` |
| 7 | `DEBUG` | | 17–24 | `ERROR` |

The original numeric severity is always preserved in `attributes._severity`.

## Query / filter

The existing log query and stats endpoints honor a `source_type` filter:

```
GET /v1/projects/{project_id}/logs?source_type=app|infra      (omit = all)
GET /v1/projects/{project_id}/logs/stats?source_type=app|infra
```

Log entries return the infra fields (`sourceType`, `sourceId`, `host`, `unit`,
`pod`, …, `attributes`). App logs read back as `source_type=app` unchanged.

## Retention

Source-type-aware deletion (run on a schedule):

```
admin retention
```

- Infra logs expire on the org plan's `infra_retention_days` (free 7, pro 14,
  ent unlimited).
- App logs expire on each project's own `retention_days`.

The two passes are independent, so changing one never affects the other.

## Observability

expvar counters surfaced by the metrics service: `ingest_accepted`,
`ingest_rejected`, `ingest_dropped`, `ingest_throttled`.

## Deferred (follow-ups)

- **Syslog-over-TLS listener** (`:6514`, RFC 5424/3164, token carried in
  structured data `[streamlogia@1 token="ls_src_live_…"]`). It needs a new
  standalone daemon under `api/services/syslog` (modeled on the `metrics`
  daemon) with in-process `crypto/tls`, since it cannot sit behind the HTTP
  nginx. The shared `business/sdk/ingest` pipeline and `sourcebus` key auth are
  already reusable by it.
- OTLP **gRPC** endpoint.
- Routing `source.quota_exceeded` through the integration/notification system
  (the current alert path is log-shaped; a non-log alert type is needed first).
- A dedicated high-volume log store (e.g. ClickHouse) if Postgres volume demands
  it — the `logbus.Storer` seam allows swapping without touching the listeners.
```
