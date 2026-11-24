# Configuration

snap has two kinds of configuration:

- **Environment variables**, read once at startup. A change needs a restart.
- **Admin settings**, stored in the database. A change applies without a restart.

A test (`internal/app/docs_test.go`) fails when a variable or setting in the code is missing here.

## Environment variables

Lists are comma-separated. Address lists take addresses (`192.168.1.1`) and CIDRs (`192.168.1.0/24`). Variables marked `/ _FILE` can instead be read from a file: set `SNAP_<NAME>_FILE` to its path (not both). The file's trailing newline is dropped.

| Variable | Default | Meaning |
|---|---|---|
| `SNAP_PUBLIC_URL` | required | The app's origin, e.g. `https://share.example.com`, without a path. Used in every link and in the CSP. Cookies are `Secure` and HSTS is sent when it is https. |
| `SNAP_RAW_URL` | — | A second origin for raw content, ideally on another registrable domain. See [uploads.md](uploads.md#separate-raw-origin). |
| `SNAP_LISTEN` | `:8080` | HTTP listener. `snap-server healthcheck` reads it too. |
| `SNAP_METRICS_LISTEN` | `:9090` | Prometheus listener (`GET /metrics`), separate from `SNAP_LISTEN`. `off` turns it off. Keep it off the public network ([observability.md](observability.md#metrics)). |
| `SNAP_METRICS_TOKEN` / `_FILE` | — | When set, scrapes must send `Authorization: Bearer <token>`. At least 16 characters. |
| `SNAP_METRICS_CIDRS` | — | Address list. When set, the metrics listener answers `403` to other peers. The connecting address counts; `X-Forwarded-For` is ignored. |
| `SNAP_DATA_DIR` | `/data` | The database (`snap.db`) and scratch files for media jobs. |
| `SNAP_FILES_DIR` | `$SNAP_DATA_DIR/files` | Uploaded files, thumbnails and teasers. |
| `SNAP_MEDIA_WORKER` | — | Run ffmpeg in a separate media worker instead of in process: `unix:///run/snap-media/media.sock` or `http://host:port`. Needs `SNAP_MEDIA_WORKER_TOKEN` ([media worker](#media-worker), [media.md](media.md#running-ffmpeg-in-a-separate-container)). |
| `SNAP_SECRETS_DIR` | `$SNAP_DATA_DIR/secrets` | One-time secret ciphertext. Keep it out of snapshots and backups ([security.md](security.md#stated-limits)). |
| `SNAP_CLI_DIR` | `/usr/share/snap/cli` | `sp` builds served at `/dl/snap-cli-<os>-<arch>`. The image fills it; missing files answer `404`. |
| `SNAP_SECRET_KEY` / `_FILE` | required | At least 32 bytes. Signs the SSO state cookie, unlock cookies and signed raw URLs, derives the stats visitor key, and encrypts saved ntfy tokens and authenticator-app secrets. Generate with `openssl rand -base64 48`. |
| `SNAP_SECRET_KEY_PREVIOUS` / `_FILE` | — | The old key during a rotation; still accepted when checking signatures and when reading saved ntfy tokens and authenticator-app secrets. |
| `SNAP_TRUSTED_PROXIES` | — | Address list of reverse proxies. `X-Forwarded-For` is read only on connections from them; the client is the rightmost address that is not a trusted proxy. Empty: the connecting address is the client. |
| `SNAP_RATELIMIT_EXEMPT` | — | Address list exempt from per-address rate limits and caps. Per-account and per-key limits still apply. |
| `SNAP_ADMIN_CIDRS` | — | Address list. When set, admin pages and the admin API answer `403` to other client addresses. |
| `SNAP_OIDC_ISSUER` | — | SSO issuer URL (https). SSO is on when set. For Gitea, its `ROOT_URL`. |
| `SNAP_OIDC_CLIENT_ID` | — | Required with the issuer. |
| `SNAP_OIDC_CLIENT_SECRET` / `_FILE` | — | Required with the issuer. |
| `SNAP_OIDC_NAME` | `SSO` | Button label: "Continue with Gitea". |
| `SNAP_OIDC_AUTO_PROVISION` | `true` | Create accounts for new SSO users whatever the sign-up mode. When false, new SSO users need an invite. |
| `SNAP_OIDC_ALLOWED_GROUPS` | — | When set, only members of these groups may sign in with SSO. |
| `SNAP_OIDC_ADMIN_GROUPS` | — | Members of these groups are admins. |
| `SNAP_OIDC_ADMIN_SUBS` | — | These provider user ids (`sub`) are admins. |
| `SNAP_OIDC_LINK_BY_EMAIL` | `false` | Link an SSO sign-in to a local account with the same verified email, if that account has no password. |
| `SNAP_SMTP_HOST` | — | SMTP server. Mail is on when set. |
| `SNAP_SMTP_PORT` | `465` | Usually 465 for `implicit`, 587 for `starttls`. |
| `SNAP_SMTP_TLS` | `implicit` | `implicit` (TLS from the start), `starttls` (upgrade required; refuses servers without it), `none` (plain text, only for a local relay such as Mailpit). |
| `SNAP_SMTP_USER` | — | Login name; no authentication when empty. |
| `SNAP_SMTP_PASSWORD` / `_FILE` | — | Password or app password. |
| `SNAP_SMTP_FROM` | — | Required with the host, e.g. `snap <snap@example.com>`. |
| `SNAP_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. |
| `SNAP_LOG_FORMAT` | `json` | App log format: `json` or `text`. |
| `SNAP_LOG_FILE` | — | Write the app log to this file instead of stdout. A relative path is under `SNAP_DATA_DIR`. Reopened on `SIGHUP`. |
| `SNAP_AUDIT_LOG` | `off` | Also stream audit entries: `off`, `stdout` (JSON records with `log=audit`, whatever the log format and level) or a file path (JSON lines; relative to `SNAP_DATA_DIR`; reopened on `SIGHUP`). Not inside the files or secrets directory ([observability.md](observability.md#audit-stream)). |

The runtime image sets `SNAP_DATA_DIR=/data`, `SNAP_LISTEN=:8080` and `SNAP_METRICS_LISTEN=:9090`. The `sp` CLI has its own variables, `SNAP_URL` and `SNAP_KEY` ([cli.md](cli.md#log-in)).

### Requests

Request bodies are limited to 1 MiB, read within 30 seconds, and multipart uploads are refused, except on the upload, chunk, paste and sealed-secret routes, which set their own limits. Headers must arrive within 10 seconds. There is no server-wide read timeout, so large uploads can take as long as they need.

### Sign-in and sessions

Session cookies are `Secure` when `SNAP_PUBLIC_URL` is https, and then named `__Host-snap_session`. Serve snap over https outside local testing. Sessions are random ids stored hashed, so rotating `SNAP_SECRET_KEY` does not sign anyone out; it does end unlocks of password-protected items and signed raw URLs unless the old key stays in `SNAP_SECRET_KEY_PREVIOUS` for a day, and it makes every returning visitor count as new in stats.

Authenticator-app secrets are encrypted with a key derived from `SNAP_SECRET_KEY`. After a rotation, each one is encrypted again with the new key the next time its owner enters a code. Keep the old key in `SNAP_SECRET_KEY_PREVIOUS` until the users with an authenticator app have signed in. A secret that neither key opens stops working: its owner signs in with a passkey or a recovery code instead (the code field isn't offered) and sets the app up again. Without either, an admin has to reset their two-factor authentication.

### SSO

Setup with Gitea: [gitea-sso.md](gitea-sso.md). The callback URL is `SNAP_PUBLIC_URL` + `/auth/oidc/callback`.

When `SNAP_OIDC_ADMIN_GROUPS` or `SNAP_OIDC_ADMIN_SUBS` is set, SSO users' roles follow them at every SSO sign-in, including demotion (never of the last active admin), and an SSO-linked admin's session keeps admin rights for 24 hours after an SSO sign-in ([accounts.md](accounts.md#single-sign-on)). When neither is set, roles are left alone.

### Email

Without `SNAP_SMTP_HOST` there are no invite, verification or password reset emails, open sign-up is unavailable, and **Send link** is not offered; invite links and links printed by the CLI still work. Admins check delivery with **Send test email** on **Admin › Settings**. Message bodies and links are never logged.

An SMTP provider with implicit TLS on port 465:

```sh
SNAP_SMTP_HOST=smtp.example.com
SNAP_SMTP_PORT=465
SNAP_SMTP_TLS=implicit
SNAP_SMTP_USER=snap@example.com
SNAP_SMTP_PASSWORD_FILE=/run/secrets/snap_smtp_password   # an app password
SNAP_SMTP_FROM=snap <snap@example.com>
```

### Media worker

`snap-server media-worker` runs ffmpeg in a container of its own ([media.md](media.md#running-ffmpeg-in-a-separate-container)). It reads only these variables, so it needs no secret key, public URL or database. `SNAP_LOG_LEVEL` and `SNAP_LOG_FORMAT` apply to it as well (`SNAP_LOG_FILE` does not: it logs to stdout), and `SNAP_FILES_DIR` defaults to `/files` there, which the worker opens read-only.

snap itself uses the worker when `SNAP_MEDIA_WORKER` is set, and then reads `SNAP_MEDIA_WORKER_TOKEN` too: both sides need the same token. Without `SNAP_MEDIA_WORKER`, snap ignores the token.

| Variable | Default | Meaning |
|---|---|---|
| `SNAP_MEDIA_WORKER_LISTEN` | required | `unix:///run/snap-media/media.sock` (the directory is made `0700`, the socket `0600`) or `host:port` for plain http. `snap-server media-worker healthcheck` reads it too. |
| `SNAP_MEDIA_WORKER_TOKEN` / `_FILE` | required | At least 32 characters, the same for the worker and snap. Every request must send it as `Authorization: Bearer <token>`. Generate with `openssl rand -hex 32`. |
| `SNAP_MEDIA_WORKER_PARALLEL` | `2` | ffmpeg and ffprobe children at once, 1–64. |

## Admin settings

Defaults are shown as the JSON values the CLI takes. **Where** says where to change a setting besides the CLI.

| Key | Default | Meaning | Where |
|---|---|---|---|
| `site.name` | `"snap"` | Name in the header, page titles and emails (1–64 characters). | Admin › Settings |
| `auth.signup_mode` | `"invite"` | `"closed"`, `"invite"` or `"open"` ([accounts.md](accounts.md#sign-up-modes)). Open needs SMTP. | Admin › Settings |
| `auth.password_login` | `true` | Allow local sign-in: a password, or a passkey without one. Can be turned off only when SSO is configured; passkeys then still work as a second step. | Admin › Settings |
| `auth.require_2fa` | `"off"` | `"off"`, `"admins"` or `"everyone"`: who must set up a second factor (a passkey or an authenticator app) at their next password sign-in, before anything else ([accounts.md](accounts.md#two-factor-authentication)). SSO sign-ins leave 2FA to the identity provider. | Admin › Settings |
| `uploads.max_bytes` | `1000000000` | Largest file, in bytes (1 GB). A user's or key's own limit can only lower it. Keep the proxy's body limit above it (`CADDY_MAX_BODY` in the example deployment). | Admin › Settings |
| `uploads.default_quota_bytes` | `0` | Storage per user, in bytes; `0` is unlimited. A user's own quota replaces it. | Admin › Settings |
| `uploads.blocked_extensions` | `[]` | File name extensions refused at upload, e.g. `["exe", "bat"]`. Letters, digits and dots, with an optional leading dot; anything else is refused. | Admin › Settings |
| `uploads.blocked_types` | `[]` | Sniffed MIME types refused at upload, e.g. `["application/x-msdownload", "video/*"]`. | Admin › Settings |
| `items.max_expiry_days` | `0` | Caps every item's expiry, including "never"; `0` is no cap. | Admin › Settings |
| `items.slug_cooldown_days` | `90` | Days a name freed by a delete, an expiry or a rename stays reserved for its previous owner, so old links never lead to someone else's content; `0` frees names at once. | Admin › Settings |
| `storage.min_free_bytes` | `1073741824` | Uploads and secrets fail with `507` rather than leave less free space than this (1 GiB) on the files or secrets disk. | Admin › Settings |
| `secrets.max_bytes` | `268435456` | Largest file secret, in bytes (256 MiB). The browser holds the whole file in memory. | Admin › Settings |
| `secrets.max_views` | `100` | Most views a secret can be given. | Admin › Settings |
| `secrets.max_expiry` | `2592000` | Longest secret lifetime, in seconds (30 days). The minimum is 5 minutes. | Admin › Settings |
| `secrets.max_failures` | `10` | Wrong passphrases before a secret is destroyed. | Admin › Settings |
| `secrets.anonymous` | `false` | Let visitors without an account create text secrets. | Admin › Settings |
| `secrets.anonymous_max_bytes` | `65536` | Largest anonymous text secret after padding (64 KiB). | Admin › Settings |
| `secrets.anonymous_max_total_bytes` | `268435456` | Ciphertext of all live anonymous secrets together (256 MiB). | Admin › Settings |
| `secrets.anonymous_per_hour` | `60` | Anonymous secrets created per hour, from all addresses together. | Admin › Settings |
| `media.teasers` | `true` | Make video hover previews. Off: new uploads get none; existing ones stay. | Admin › Settings |
| `stats.retention_days` | `90` | Days raw item and secret events (with IPs and User-Agents) and in-app notifications are kept; 1–3650. Daily totals stay. | Admin › Settings |
| `email.send_links` | `true` | Offer **Send link** on the dashboard (needs SMTP). | Admin › Settings |
| `email.send_links_per_day` | `20` | Emails one user can send with **Send link** per 24 hours. | Admin › Settings |
| `email.send_links_site_per_day` | `200` | Emails the whole server sends with **Send link** per 24 hours. | Admin › Settings |
| `notify.allow_http` | `false` | Allow plain `http://` ntfy servers. Off: only https ([notifications.md](notifications.md)). | Admin › Settings |
| `notify.outbound_allow` | `[]` | Hosts, addresses or CIDRs notifications may reach even though they are private, loopback or link-local (e.g. `"ntfy"` for a container on the same network). | Admin › Settings |
| `metrics.per_user` | `false` | Export per-user series: storage, items and requests by user id (up to 500 users) ([observability.md](observability.md#opt-in-series)). | Admin › Settings |
| `metrics.per_key` | `false` | Export requests by API key id (up to 500 keys). | Admin › Settings |
| `metrics.referrers` | `false` | Export visitor events of the last day for the 20 busiest referrer hosts. | Admin › Settings |

Admin › Settings also sends test emails and creates invites.

### From the CLI

```sh
docker compose exec snap snap-server admin settings list
docker compose exec snap snap-server admin settings get uploads.max_bytes
docker compose exec snap snap-server admin settings set uploads.max_bytes 5000000000
docker compose exec snap snap-server admin settings set uploads.blocked_extensions '["exe", "bat"]'
docker compose exec snap snap-server admin settings set site.name '"My share"'
```

Values are JSON: strings in double quotes, lists in brackets. Values outside a setting's range are refused. Each change is written to the audit log. A running server picks it up within a minute.
