# Deployment

`deploy/` holds an example deployment: snap and Caddy in one compose project. Caddy gets certificates from Let's Encrypt and proxies to snap; snap publishes no ports. Every variable and setting is listed in [configuration.md](configuration.md).

This page is the reference for that example. [self-hosting.md](self-hosting.md) is the guide that puts it together: a separate raw domain, a proxy you already run, Prometheus, the audit stream, a backup script and upgrades.

| File | Purpose |
|---|---|
| `deploy/compose.yml` | the two services, bind mounts and the network |
| `deploy/caddy/Caddyfile` | TLS, request body limit, proxy, filtered access log (mounted as the directory `/etc/caddy`) |
| `deploy/.env.example` | the settings an operator sets; copy to `.env` |
| `deploy/compose.local-test.yml` | the [local test](#local-test) variant |

## Requirements

- Docker Engine 23 or later with the compose plugin.
- A host name (`share.example.com`) whose DNS points at the host, with ports 80 and 443 reachable from the internet for Let's Encrypt. Nothing else may listen on 80 or 443.
- Optionally a second name for the [raw origin](#raw-origin), an SMTP account and an OIDC provider.

## The image

The image runs as uid 10001 with a read-only root filesystem. It contains `snap-server`, ffmpeg and the `sp` builds served at `/dl/`.

- **Published:** CI pushes `ghcr.io/battleroid/snap-share:<version>` (e.g. `v0.1.0`) and `:latest` for every `v*` tag. Pin a version in `SNAP_IMAGE`. If the registry asks for credentials, run `docker login ghcr.io` first.
- **Built locally**, from the repository root:

  ```sh
  make image IMAGE=snap:local
  # or, without make:
  docker build --build-arg VERSION="$(git describe --always)" -t snap:local .
  ```

  Then set `SNAP_IMAGE=snap:local`.

## First run

All commands run in `deploy/` of a clone of the repository (`git clone https://github.com/Battleroid/snap-share.git`), or in a copy of that directory.

1. Create `.env`:

   ```sh
   cp .env.example .env
   chmod 600 .env
   sed -i "s|^SNAP_SECRET_KEY=.*|SNAP_SECRET_KEY=$(openssl rand -base64 48)|" .env
   ```

   Edit `.env` and set at least `SNAP_IMAGE` and `SNAP_DOMAIN`.

2. Create the data directories, owned by the container's user:

   ```sh
   sudo install -d -o 10001 -g 10001 -m 0700 data files secrets
   ```

   | Directory | Holds | Backups |
   |---|---|---|
   | `data` | the database `snap.db` | yes, with `snap-server backup` |
   | `files` | uploads, thumbnails, teasers | yes |
   | `secrets` | one-time secret ciphertext | **no**, see [Backups](#backups) |

   To put one elsewhere, set `DATA_PATH`, `FILES_PATH` or `SECRETS_PATH` in `.env`.

3. Start the stack:

   ```sh
   docker compose up -d --wait
   ```

   snap applies its database migrations, then Caddy starts and requests the certificate. `docker compose logs caddy` shows the result.

4. Create the first admin:

   ```sh
   docker compose exec snap snap-server admin create-user --username alice --email alice@example.com --admin
   ```

   It prints a link to set the password, valid once for 24 hours. To set the password directly instead:

   ```sh
   printf '%s\n' 'a long password' | docker compose exec -T snap snap-server admin create-user --username alice --admin --password-stdin
   ```

5. Open `https://share.example.com/` and sign in. Sign-up is invite-only by default ([accounts.md](accounts.md#sign-up-modes)).

## DNS and TLS

Point an A record (and AAAA, if the host has IPv6) for `SNAP_DOMAIN` at the host before the first start. Caddy obtains and renews certificates by itself and keeps them in `caddy-data/`. To get expiry notices, uncomment `email` in the Caddyfile's global block.

A host that is not reachable from the internet can use Caddy's own CA instead: set `CADDY_TLS=tls internal` in `.env` and install the root certificate from `caddy-data/caddy/pki/authorities/local/root.crt` on the clients. Browsers need a trusted certificate: one-time secrets and the clipboard work only in a secure context.

## Reverse proxy

The example's Caddy needs no changes. With another proxy in front of snap, or instead of Caddy:

- **Client address.** Set `SNAP_TRUSTED_PROXIES` to the proxy's address or subnet. snap then reads `X-Forwarded-For` from the right and takes the first address that is not a trusted proxy. Without it every request seems to come from the proxy, so rate limits and stats treat all visitors as one. The proxy must add the connecting address to `X-Forwarded-For` (append or replace); one that forwards the header unchanged lets clients choose their address. In the example the compose network's subnet is pinned in `compose.yml` and repeated in `.env`.
- **NAT hairpin.** Visitors on the LAN who use the public name often arrive from the router's address (`192.168.1.1`). Put that address in `SNAP_RATELIMIT_EXEMPT` so they don't share one rate limit.
- **Host header.** Pass it unchanged. snap tells the app and raw origins apart by it.
- **Body size.** Allow at least `uploads.max_bytes` (default 1 GB) plus a few KB for multipart headers. The example's Caddy reads its limit from `CADDY_MAX_BODY` (default `1GiB`). The browser uploads in 8 MiB parts and secrets in 1 MiB chunks, but curl, ShareX and `sp up` send a file in one request. nginx defaults to 1 MB (`client_max_body_size`); some CDNs cap bodies at 100 MB.
- **Buffering and timeouts.** Stream request bodies instead of buffering them (nginx: `proxy_request_buffering off`). A 1 GB upload at 10 Mbit/s takes about 15 minutes, so no timeout may cover the whole request. Caddy sets none by default; snap has only a 10 s limit for request headers. The notification stream (`/notifications/stream`, `text/event-stream`) must not be buffered either: Caddy flushes it at once, nginx honours its `X-Accel-Buffering: no`. It sends a comment every 25 s and ends after 5 minutes, so a read timeout of a minute or more is enough.
- **Metrics.** Never proxy `SNAP_METRICS_LISTEN`.

## Raw origin

Set `SNAP_RAW_DOMAIN` in `.env` and point its DNS at the host. Compose sets `SNAP_RAW_URL=https://<raw domain>` and Caddy serves both names. snap then serves raw files, downloads, thumbnails and teasers from that origin, signs URLs for private and password-protected items, and previews PDFs inline ([uploads.md](uploads.md#separate-raw-origin)).

Use a separate registrable domain (`example-content.net` for `share.example.com`), not a subdomain: a subdomain is same-site with the app, so SameSite cookie rules do not separate the two. Raw responses are sandboxed either way; the raw origin is a second layer.  The guide gives the reasoning: [self-hosting.md](self-hosting.md#why-a-second-domain).

## SSO

Create the OAuth2 application and set the `SNAP_OIDC_*` variables in `.env` as described in [gitea-sso.md](gitea-sso.md), then `docker compose up -d`. The snap container must reach the issuer URL (DNS, and hairpin NAT if the provider runs on the same network).

## Email

Set the `SNAP_SMTP_*` variables in `.env` ([configuration.md](configuration.md#email)) and `docker compose up -d`. Check delivery with **Send test email** on **Admin › Settings**. Use an app password made for snap alone, so it can be revoked without affecting anything else.

## Metrics

snap serves Prometheus metrics at `:9090/metrics` on the compose network. The port is not published and Caddy does not proxy it. To scrape it, attach Prometheus to the network (`snap_default`, as an external network in Prometheus's compose file) and use the target `snap:9090`. `SNAP_METRICS_TOKEN` adds a bearer token and `SNAP_METRICS_CIDRS` limits the peers that may scrape. The metric reference, the opt-in per-user, per-key and referrer series, and a scrape config are in [observability.md](observability.md#metrics).

## Logs

snap writes JSON lines to stdout (`docker compose logs -f snap`) with a `level` field (`SNAP_LOG_FORMAT=text` for logfmt, `SNAP_LOG_FILE` for a file). A request line has a request id, the method, route pattern, status, bytes, duration, client address, principal kind, and user or key id; never the path. Query strings, headers, cookies, tokens, secret ids and email bodies are never logged.

`SNAP_AUDIT_LOG=stdout` adds the audit log to the same output as records tagged `log=audit`, or `SNAP_AUDIT_LOG=audit/audit.jsonl` writes it to a file under `/data`. Both are meant for a log shipper such as Vector ([observability.md](observability.md#audit-stream)); Admin › Audit keeps working from the database either way.

Caddy's access log (`docker compose logs caddy`) uses the filter in the Caddyfile, which replaces secret ids, zip tokens and query strings with `REDACTED` and drops the `X-Snap-*` headers and `Location`. Keep a filter like it if you change the log setup.

Docker's default log driver keeps container logs without limit. Set log rotation in `/etc/docker/daemon.json` or a `logging:` block.

## Backups

Back up, in this order:

1. **The database**, with a consistent copy taken while snap runs:

   ```sh
   docker compose exec snap snap-server backup /data/backup-$(date +%F).db
   ```

   The file appears in `data/` and must not exist yet. Move it off the host.
2. **`files/`**, with any file-level tool (rsync, restic, ZFS snapshots). Copying it after the database means every item in the copy has its file.
3. **`.env`**, kept somewhere private: it holds `SNAP_SECRET_KEY` and the other credentials.

**Leave `secrets/` out** of backups and snapshots. Burning a secret deletes its ciphertext file; a copy that survives in a backup or snapshot can be decrypted by anyone who also has the link, so "burned" would not mean gone. Put `secrets/` on storage without snapshots (`SECRETS_PATH`) and exclude it from backup jobs. The cost: unopened secrets do not survive a restore.

The database copy still holds secret metadata: sizes, times, owner notes and tombstones (without verifiers). For secrets not yet opened when it was made, it also holds the passphrase verifier, so someone with the copy and a link can test passphrases for that secret offline at the Argon2id cost. Keep database backups no longer than you need. Details: [security.md](security.md#stated-limits).

Deleted uploads stay in file backups and snapshots until those expire.

## Restore

```sh
docker compose down
sudo rm -f data/snap.db data/snap.db-wal data/snap.db-shm
sudo install -o 10001 -g 10001 -m 0600 /path/to/backup-2026-11-22.db data/snap.db
sudo rsync -a --delete /path/to/files-backup/ files/
sudo chown -R 10001:10001 files
docker compose up -d --wait
```

Restore `.env` too, or create a new `SNAP_SECRET_KEY`: a new key only ends unlocks of password-protected items and signed raw URLs, resets unique-visitor counting, and makes saved ntfy access tokens unreadable (users re-enter them; to avoid that, keep the old key in `SNAP_SECRET_KEY_PREVIOUS` while they do). Secrets whose ciphertext is not in `secrets/` can no longer be opened. Storage usage is recounted within an hour.

## Upgrades

1. Read the release notes and take a database backup.
2. Change the tag in `SNAP_IMAGE` (or rebuild `snap:local`).
3. Pull and restart:

   ```sh
   docker compose pull snap
   docker compose up -d --wait
   ```

snap applies new migrations at start and logs each as `migration applied`; `snap-server migrate` applies them without starting the server. Migrations only go forward: to go back to an older version, restore the backup taken before the upgrade. The Caddy image is pinned in `compose.yml`; upgrade it the same way.

## Resources

- **Memory.** ffmpeg runs at most 2 processes at once, each limited to 1 GiB of address space, 120 s of CPU time, 60 s of wall time and 2 threads. In-process image decodes are limited to 100 megapixels (about 400 MB each, also 2 at once). Allow about 3 GiB at peak; a container memory limit below that can get snap killed during media jobs. Details: [media.md](media.md#how-ffmpeg-is-constrained).
- **CPU.** Media jobs run up to 2 ffmpeg processes with 2 threads each; requests are served alongside.
- **Disk.** Uploads and secrets fail with `507` when they would leave less than `storage.min_free_bytes` (1 GiB) free. The database stays small; media jobs keep temporary files in `data/media-work`. `/tmp` is a tmpfs; uploads are streamed to `files/`, not to `/tmp`.

## Local test

This checks the example without DNS, without Let's Encrypt and without binding host ports: `compose.local-test.yml` switches Caddy to its own CA, publishes nothing and renames the project to `snap-local-test`. Requests come from a throwaway curl container on the stack's network. Run it in a fresh copy of the repository, not in a live deployment's directory.

```sh
make image IMAGE=snap:local
cd deploy
cp .env.example .env
chmod 600 .env
sed -i "s|^SNAP_IMAGE=.*|SNAP_IMAGE=snap:local|; s|^SNAP_DOMAIN=.*|SNAP_DOMAIN=snap.test|; s|^SNAP_SECRET_KEY=.*|SNAP_SECRET_KEY=$(openssl rand -base64 48)|" .env
sudo install -d -o 10001 -g 10001 -m 0700 data files secrets
dc() { docker compose -f compose.yml -f compose.local-test.yml "$@"; }
dc up -d --wait
printf '%s\n' 'local test password' | dc exec -T snap snap-server admin create-user --username admin --admin --password-stdin

mkdir local-test
dc exec -T caddy cat /data/caddy/pki/authorities/local/root.crt > local-test/ca.crt
t() { docker run --rm --network snap-local-test_default -u "$(id -u)" -v "$PWD/local-test:/w" -w /w \
        curlimages/curl:8.17.0 -sS --cacert ca.crt "$@"; }

t -o /dev/null -w '%{http_code}\n' https://snap.test/        # 200: the sign-in page
t https://snap.test/healthz                                  # ok
t -c jar -o /dev/null --data-urlencode login=admin --data-urlencode 'password=local test password' https://snap.test/login
key=$(t -b jar -H 'Content-Type: application/json' -d '{"name":"local test","scopes":["items:read","items:write"]}' \
        https://snap.test/api/v1/keys | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
echo 'hello from the local test' > local-test/hello.txt
url=$(t -H "Authorization: Bearer $key" -F file=@hello.txt 'https://snap.test/api/v1/upload?format=url')
t -o /dev/null -w '%{http_code}\n' "$url"                     # 200: the share page
t "$url/raw"                                                 # hello from the local test
```

Tear it down:

```sh
dc down
sudo rm -rf data files secrets caddy-data caddy-config local-test .env
```

## Troubleshooting

| Symptom | Cause |
|---|---|
| snap restarts with `open /data/snap.db: unable to open database file (14)` | `data/` is not writable by uid 10001; see step 2 of [First run](#first-run). |
| snap exits with `SNAP_SECRET_KEY must be at least 32 bytes` | `SNAP_SECRET_KEY` is empty or short in `.env`. |
| `dependency failed to start: container … is unhealthy` | snap did not start; `docker compose logs snap` says why. |
| Caddy logs ACME errors | DNS does not point at the host yet, or ports 80/443 are not reachable from the internet. |
| `Pool overlaps with other one on this address space` | The pinned subnet in `compose.yml` collides with another network; change it there and in `SNAP_TRUSTED_PROXIES`. |
| Large uploads fail with `413` before reaching snap | A proxy's body limit is below `uploads.max_bytes`. |
| Every visitor has the same address in logs and stats | `SNAP_TRUSTED_PROXIES` does not match the proxy's address. |
