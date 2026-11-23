# Self-hosting snap

A guide to running snap for yourself or a small group: behind a reverse proxy, with a separate origin for uploaded content, monitoring, backups and upgrades. It builds on the example deployment in `deploy/` and links to the reference for each part instead of repeating it. [deployment.md](deployment.md) has the step-by-step first run.

The names below are placeholders: `share.example.com` is the app, `raw.example.net` the raw content origin, and `192.0.2.10` (a documentation address) stands in for your server.

## What you end up with

```
                     80, 443
 browsers, curl, sp ────────► Caddy ──► snap:8080   the app and the raw origin (told apart by Host)
                              (TLS)     snap:9090   metrics: the compose network only
                                           │
                  Prometheus ──────────────┘        scrapes snap:9090 with a bearer token
                  log shipper ◄── docker logs       app log and audit stream
                                           │
                                     data/ files/ secrets/
```

Only the proxy publishes ports. snap runs as an unprivileged user with a read-only root filesystem and keeps its state in three directories.

## Domains and DNS

Point an A record (and AAAA, if the server has IPv6) for each name at the server:

| Name | Points at | Used for |
|---|---|---|
| `share.example.com` | `192.0.2.10` | the app: sign-in, dashboard, share pages, API |
| `raw.example.net` | `192.0.2.10` | uploaded content only: raw files, downloads, thumbnails, teasers |

Ports 80 and 443 (TCP, and UDP for HTTP/3) must be reachable from the internet so the proxy can get certificates. A server that is not public can use another certificate source: see [DNS and TLS](deployment.md#dns-and-tls).

### Why a second domain

Anything a user uploads is served as it was uploaded: an HTML or SVG file is content you didn't write. snap sandboxes it with a restrictive CSP and safe content types ([uploads.md](uploads.md#how-content-is-served)), and a separate raw origin adds a second layer. On the raw origin, cookies and `Authorization` headers are dropped before the app sees them, no response sets a cookie, and every other path redirects to the app ([uploads.md](uploads.md#separate-raw-origin)).

Use a different registrable domain (`raw.example.net` for `share.example.com`), not a subdomain such as `raw.share.example.com`. A subdomain is same-site with the app, so the browser's SameSite cookie rules would not separate the two ([deployment.md](deployment.md#raw-origin)). Without a raw origin snap serves content from the app's own origin. That works, but loses this layer, and PDFs get an Open and Download button instead of an inline preview.

## Compose and the proxy

Copy `deploy/` to the server (or clone the repository and work in `deploy/`) and follow [First run](deployment.md#first-run). In `.env` set at least:

```sh
SNAP_IMAGE=ghcr.io/battleroid/snap-share:v0.1.0   # a release tag; see Upgrades below
SNAP_DOMAIN=share.example.com
SNAP_RAW_DOMAIN=raw.example.net
SNAP_SECRET_KEY=<the output of: openssl rand -base64 48>
```

Compose then gives snap `SNAP_PUBLIC_URL` and `SNAP_RAW_URL`, and Caddy serves both names from one site block. It gets a certificate for each name, limits request bodies, streams uploads to snap and writes an access log with secret ids and query strings removed (`deploy/caddy/Caddyfile`). The compose network's subnet is pinned and named in `SNAP_TRUSTED_PROXIES`, so snap takes visitors' addresses from the `X-Forwarded-For` header Caddy sets.

### A proxy you already run

To put snap behind a proxy that already owns ports 80 and 443, remove the `caddy` service from the compose file and follow the rules in [Reverse proxy](deployment.md#reverse-proxy): list only the proxy in `SNAP_TRUSTED_PROXIES`, pass `Host` unchanged, allow bodies of at least `uploads.max_bytes`, stream request bodies, and never proxy the metrics listener. The proxy must be able to reach snap's port 8080: attach it to the compose network, or publish the port on an address only the proxy can reach, never on `0.0.0.0`. `SNAP_TRUSTED_PROXIES` must name the address snap sees the proxy connect from.

A Caddy site block for both names:

```caddyfile
share.example.com, raw.example.net {
	request_body {
		max_size 1GiB
	}
	reverse_proxy snap:8080
}
```

The proxy's access log is the easiest place to leak a secret. snap's own log never carries a path or query string, but a proxy's default one does: nginx's `access_log` records the request line, which includes secret ids (`/s/...`) and signed-URL parameters. Turn the access log off for these names, or filter it as `deploy/caddy/Caddyfile` does.

## Metrics with Prometheus

snap serves Prometheus metrics on a separate listener (`:9090`) that the proxy never forwards. Add a token so that only your Prometheus can read it, in `.env`:

```sh
SNAP_METRICS_TOKEN=<the output of: openssl rand -hex 24>   # at least 16 characters
```

Run Prometheus in its own compose project and attach it to snap's network, `snap_default`:

```yaml
name: monitoring
services:
  prometheus:
    image: prom/prometheus:v3.5.0   # pin the release you use
    restart: unless-stopped
    volumes:
      - ./prometheus.yml:/etc/prometheus/prometheus.yml:ro
      - ./snap_metrics_token:/etc/prometheus/secrets/snap_metrics_token:ro
      - prometheus-data:/prometheus
    networks: [snap]
volumes:
  prometheus-data:
networks:
  snap:
    name: snap_default
    external: true
```

Write the same token to `snap_metrics_token` with `printf '%s'` (no trailing newline) and make the file readable by the container's user. The scrape config, every metric and the cost of the optional per-user series are in [observability.md](observability.md#prometheus); the target is `snap:9090`, and a scrape interval of 30 s or more is enough. `SNAP_METRICS_CIDRS` can additionally limit which peers may scrape ([configuration.md](configuration.md#environment-variables)).

## Logs and the audit stream

snap writes its app log as JSON lines on stdout. Set `SNAP_AUDIT_LOG=stdout` in `.env` to add the audit log (sign-ins, role and settings changes, key creation, moderation) to the same output as records tagged `log=audit`, or give a file path under `/data` to write it to a file. A log shipper such as Vector can split the two and send the audit records to storage that snap cannot edit: [observability.md](observability.md#vector) has working configurations for both ways. Admin › Audit keeps working from the database either way.

Docker's default log driver keeps container logs without limit; set rotation in `/etc/docker/daemon.json` or a `logging:` block ([Logs](deployment.md#logs)).

## Backups

State lives in three host directories next to `compose.yml`:

| Directory | Back up | How |
|---|---|---|
| `data/` | yes | with `snap-server backup`, which writes a consistent copy while snap runs. Don't copy `snap.db` itself |
| `files/` | yes | any file-level tool (rsync, restic, a filesystem snapshot), after the database copy |
| `secrets/` | **no** | one-time secret ciphertext; a copy that outlives the secret undoes "burned" ([Backups](deployment.md#backups)) |

Keep `.env` somewhere private too: it holds `SNAP_SECRET_KEY`. A nightly script, run by a user who may use Docker and sudo:

```sh
#!/bin/sh
set -eu
cd /srv/snap/deploy                  # where compose.yml lives
out=/var/backups/snap
name=snap-$(date +%F).db

docker compose exec -T snap snap-server backup "/data/$name"   # FILE must not exist yet
sudo mv "data/$name" "$out/$name"                              # data/ belongs to uid 10001
sudo rsync -a --delete files/ "$out/files/"                    # after the database, never before
```

Then copy `$out` off the machine. Restoring is described under [Restore](deployment.md#restore); try it once on a spare machine before you need it.

## Upgrades

Pin a release tag in `SNAP_IMAGE` instead of `:latest`, so a routine `docker compose pull` cannot change the version. Every `v*` tag is published as `ghcr.io/battleroid/snap-share:<tag>`. To upgrade:

1. Read the release notes and take a database backup (above).
2. Change the tag in `.env`.
3. Run `docker compose pull snap && docker compose up -d --wait`.

snap applies new database migrations at start. They only go forward: to go back, restore the backup from step 1 and set the old tag. More in [Upgrades](deployment.md#upgrades). The Caddy image is pinned in `compose.yml` and upgrades the same way.

## After the first start

- Create the first admin ([accounts.md](accounts.md#creating-the-first-admin)). Sign-up is invite-only until you change it ([sign-up modes](accounts.md#sign-up-modes)).
- Set up outgoing mail for invites and password resets ([Email](deployment.md#email)), and optionally sign-in through an OIDC provider ([gitea-sso.md](gitea-sso.md)).
- Visitors on your own network who use the public name often reach snap from your router's address and share one rate limit; see the NAT hairpin note under [Reverse proxy](deployment.md#reverse-proxy).
- If you administer only from one network, restrict the admin area to it with `SNAP_ADMIN_CIDRS` ([admin.md](admin.md)).
