# Observability

snap has three outputs for monitoring: the app log, an optional audit stream, and Prometheus metrics on a separate listener. None of them carries passwords, tokens, keys, cookies, `Authorization` headers, query strings or secret ids.

## App log

One record per line on stdout (`SNAP_LOG_FORMAT=json`, the default, or `text`), at `SNAP_LOG_LEVEL`. `SNAP_LOG_FILE` writes it to a file instead; `SIGHUP` reopens the file after logrotate moved it.

Each request gets one `request` record:

| Field | Value |
|---|---|
| `request_id` | 16 hex characters, made by the server (an incoming `X-Request-Id` is ignored); also the `X-Request-Id` response header. Records logged with the request's context, such as a panic, carry it too. |
| `method`, `route` | The method and the route pattern, e.g. `GET /s/{id}`; `unmatched` when no route matched. Never the path or query string. |
| `status`, `bytes`, `ms` | Response status, body bytes, duration in milliseconds. |
| `ip` | The client address (after `SNAP_TRUSTED_PROXIES`). |
| `principal` | `anonymous`, `session` or `key`. |
| `user`, `key` | The user id and API key id, when there is one. |

Background jobs log `job done` (info) or `job failed` (warn) with `kind`, `id`, `attempt` and `ms`. Periodic tasks log `periodic task failed` (error) with `task` and `ms`; successful runs log at debug.

## Audit stream

Every entry of the audit log (Admin › Audit) can also be sent to a log shipper. `SNAP_AUDIT_LOG` picks where:

| Value | Output |
|---|---|
| `off` | Nothing (the default). |
| `stdout` | JSON records on stdout with `"log":"audit"`, beside the app log. They are written whatever `SNAP_LOG_FORMAT` and `SNAP_LOG_LEVEL` say. |
| a path | JSON lines appended to a file (mode 0640). A relative path is under `SNAP_DATA_DIR`; paths inside `SNAP_FILES_DIR` or `SNAP_SECRETS_DIR` are refused. `SIGHUP` reopens it. |

The stream reads committed rows back from the database, about once a second. An entry appears only after its transaction commits, never for one that rolled back, and entries made with `snap-server admin` (another process) appear too. Entries are sent as they were inserted; a later edit to a row (the failure count of a Send link entry) is not sent again. Entries committed before the server started, and in the last second before a crash, are not sent. The database table stays the record; the stream is a copy.

A line (version 1):

```json
{"v":1,"id":42,"ts":"2026-11-23T10:00:00.123Z","action":"user.role_change","actor_user":1,"actor_key":null,"ip":"192.0.2.7","target":"user:9","details":{"from":"user","to":"admin"}}
```

| Field | Value |
|---|---|
| `v` | Schema version. Fields may be added within a version; a removed or changed field means a new version. |
| `id` | The audit row id. It only grows, so a shipper can drop duplicates. |
| `ts` | When the entry was made, RFC 3339 UTC with milliseconds. |
| `action` | E.g. `auth.login`, `auth.login_failed`, `user.create`, `key.create`, `settings.change`, `moderation.burn`. |
| `actor_user`, `actor_key` | Who did it, or `null` (the CLI, an anonymous visitor). |
| `ip` | The actor's address, or `null`. |
| `target` | What it was done to, e.g. `user:9`, `invite:3`, `settings.name`, or `null`. |
| `details` | An object; its keys depend on the action. |

With `stdout`, the record also has `"level":"INFO","msg":"audit","log":"audit"` and no `time` (use `ts`).

Before a line is written, secret ids in `target` become `secret:[redacted]`, values under keys that name a token, secret, password, passphrase, code, cookie or credential become `[redacted]`, email addresses become `[email]`, and URLs lose their query string and fragment.

## Metrics

`SNAP_METRICS_LISTEN` (default `:9090`) serves `GET /metrics` on its own listener. The public listener and the raw origin never serve it, and `metrics` is a reserved name no item can take. Keep the listener off the public network. Two optional checks:

- `SNAP_METRICS_TOKEN` (or `SNAP_METRICS_TOKEN_FILE`): scrapes must send `Authorization: Bearer <token>`; others get `401`.
- `SNAP_METRICS_CIDRS`: peers outside these addresses get `403`. The TCP peer address counts, never `X-Forwarded-For`.

Gauges read from the database are cached for 30 seconds, so a scrape interval of 30 s or more is enough.

### Reference

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `snap_build_info` | gauge | `version`, `goversion` | Always 1. |
| `snap_http_requests_total` | counter | `route`, `code` | Requests by route pattern and status. |
| `snap_http_request_duration_seconds` | histogram | `route` | Request duration. |
| `snap_auth_attempts_total` | counter | `method`, `result` | Sign-ins (`password`, `totp`, `recovery`, `passkey`, `sso`, `register`, `reset`) and API key checks (`key`); `result` is `ok` or `failed`. |
| `snap_ratelimit_hits_total` | counter | `limiter` | Refusals by rate limiter, e.g. `login_ip`, `login_account`, `slug_miss`, `api_key`, `secret_open_ip`. |
| `snap_uploads_total` | counter | `target`, `kind` | Items (`item`: `file`, `paste`, `link`, `album`, `gist`) and secrets (`secret`: `text`, `file`) created. |
| `snap_upload_bytes_total` | counter | `target`, `kind` | Bytes they stored (ciphertext for secrets). |
| `snap_job_runs_total` | counter | `kind`, `result` | Queued job attempts: `ok`, `retry` or `failed` (no attempts left). |
| `snap_job_duration_seconds` | histogram | `kind` | Job attempt duration. `media.process` is the media job. |
| `snap_jobs` | gauge | `kind`, `state` | Jobs in the queue by state (`queued`, `running`, `done`, `failed`; finished jobs are kept a week). |
| `snap_task_runs_total` | counter | `task`, `result` | Periodic task runs (sweepers, flushes). |
| `snap_task_duration_seconds` | histogram | `task` | Periodic task duration. |
| `snap_media_step_duration_seconds` | histogram | `step`, `result` | ffmpeg steps: `probe`, `poster`, `teaser`, `thumbnail`. |
| `snap_notify_deliveries_total` | counter | `channel`, `result` | Notification deliveries by `email` or `ntfy`. |
| `snap_stats_events_total` | counter | `kind` | Visitor events recorded, by kind ([stats.md](stats.md)). |
| `snap_stats_writer_events_total` | counter | `outcome` | What the stats writer did with them: `written`, `dropped`, `invalid`, `failed`, `orphaned`. |
| `snap_users` | gauge | `status`, `role` | Accounts. |
| `snap_items` | gauge | `kind` | Items. |
| `snap_item_bytes` | gauge | `kind` | Bytes stored by items. |
| `snap_secrets` | gauge | `state` | Secrets by state (tombstones of burned ones included). |
| `snap_secrets_active` | gauge | — | Secrets that can still be opened. |
| `snap_secret_bytes` | gauge | — | Ciphertext of secrets uploading or ready. |
| `snap_sessions_active` | gauge | — | Unexpired browser sessions. |
| `snap_api_keys_active` | gauge | — | API keys neither revoked nor expired. |
| `snap_storage_used_bytes` | gauge | — | Bytes charged to user quotas. |
| `snap_disk_free_bytes` | gauge | `store` | Free space for `files`, `secrets` and `data`. |
| `snap_db_size_bytes`, `snap_db_wal_size_bytes` | gauge | — | SQLite database and WAL file sizes. |
| `snap_instance_scrape_errors_total` | counter | — | Failed reads of the gauges above (the last values are served). |
| `snap_instance_scrape_timestamp_seconds` | gauge | — | When they were last read. |

Plus the standard `go_*` and `process_*` collectors. Values outside a label's set are counted as `other`.

### Opt-in series

Three admin settings (Admin › Settings › Metrics, off by default) add series with a label per user, key or host:

| Setting | Series |
|---|---|
| `metrics.per_user` | `snap_user_storage_bytes{user_id}` and `snap_user_items{user_id}` for the 500 users with the most stored bytes; `snap_user_requests_total{user_id}` for up to 500 users, the rest as `other`. |
| `metrics.per_key` | `snap_key_requests_total{key_id}` for up to 500 keys, the rest as `other`. |
| `metrics.referrers` | `snap_referrer_events_24h{host}`: visitor events of the last day for the 20 busiest referrer hosts, the rest as `other`. |

Labels are numeric ids, never usernames or emails; map them with Admin › Users. Request counters count from when the setting was turned on (or the server started), and turning a setting off removes its series at the next scrape. No series is ever per item.

### Cardinality

Without the opt-in settings the number of series is fixed by the code: routes × status codes, job kinds, limiters and the label sets above. It does not grow with users, items or visitors. Each opt-in setting adds up to about 500 series (`metrics.referrers` about 20), and per-user and per-key series churn as accounts and keys come and go. Turn them on when the Prometheus storage can take it, or for a while to look into something.

## Vector

### Audit stream from docker logs

With `SNAP_AUDIT_LOG=stdout`, route the audit records out of the container's logs:

```toml
[sources.snap]
type = "docker_logs"
include_containers = ["snap"]

[transforms.snap_parsed]
type = "remap"
inputs = ["snap"]
source = '''
. = merge(., object!(parse_json(.message) ?? {}))
'''

[transforms.snap_route]
type = "route"
inputs = ["snap_parsed"]
route.audit = '.log == "audit"'
route.app = '.log != "audit"'

[sinks.audit]
type = "file"
inputs = ["snap_route.audit"]
path = "/var/log/snap/audit-%Y-%m-%d.jsonl"
encoding.codec = "json"

[sinks.app]
type = "loki"   # or anything else
inputs = ["snap_route.app"]
endpoint = "http://loki:3100"
labels.app = "snap"
encoding.codec = "json"
```

With `SNAP_LOG_FORMAT=text`, the app records are logfmt (`parse_logfmt`); audit records are JSON either way.

### Audit stream from a file

With `SNAP_AUDIT_LOG=audit/audit.jsonl` (under `/data`):

```toml
[sources.snap_audit]
type = "file"
include = ["/srv/snap/data/audit/audit.jsonl"]
read_from = "beginning"

[transforms.snap_audit_parsed]
type = "remap"
inputs = ["snap_audit"]
source = '''
. = parse_json!(.message)
'''
```

Rotate it with logrotate and signal snap afterwards:

```
/srv/snap/data/audit/audit.jsonl {
    daily
    rotate 90
    compress
    delaycompress
    postrotate
        docker kill --signal=HUP snap
    endscript
}
```

## Prometheus

```yaml
scrape_configs:
  - job_name: snap
    scrape_interval: 30s
    authorization:
      type: Bearer
      credentials_file: /etc/prometheus/secrets/snap_metrics_token
    static_configs:
      - targets: ["snap:9090"]
```

Without `SNAP_METRICS_TOKEN`, drop the `authorization` block.
