# snap

snap is a self-hosted server for sharing files, images, video, pastes, short links and one-time secrets. It is one Go binary with a SQLite database, run as a Docker image behind a reverse proxy. Content gets short links with optional passwords and expiry; one-time secrets are encrypted on the sender's machine and destroyed after a set number of views. Uploads work from the browser, curl, ShareX and the `sp` command line client.

## Features

- Uploads of any file type up to 1 GB by default, resumable in the browser, with per-user quotas.
- Share pages for images, video, audio, Markdown and highlighted code; raw, download and thumbnail URLs.
- Pastes and short links in the same namespace; names that are random, three words, the file name, or chosen.
- Unlisted or private items, passwords, expiry (delete or hide), deletion URLs.
- Thumbnails, video posters and hover previews, made by a sandboxed ffmpeg; location data stripped from JPEGs.
- Link previews for chat apps (OpenGraph), with nothing shown for private or protected items.
- One-time secrets (text or files up to 256 MiB): encrypted in the browser or by `sp`, optional passphrase, view limit, expiry, a timeline of opens.
- A dashboard with search, filters, bulk actions, zip downloads, tags, albums and sending links by email.
- Stats per item: views, unique visitors, downloads, plays, and which chat apps fetched a preview.
- Local accounts and SSO (OIDC, documented for Gitea); invite-only, open or closed sign-up.
- API keys with scopes, expiry, allowed networks and limits; a JSON API.
- An optional separate origin for raw content.
- Admin settings, an audit log and Prometheus metrics.

## Quick start

You need Docker with the compose plugin, a server with ports 80 and 443 open, and a host name that points at it. snap runs as the published image `ghcr.io/battleroid/snap-share` behind Caddy, which gets the certificate:

```sh
git clone https://github.com/Battleroid/snap-share.git
cd snap-share/deploy
cp .env.example .env && chmod 600 .env
sed -i "s|^SNAP_SECRET_KEY=.*|SNAP_SECRET_KEY=$(openssl rand -base64 48)|" .env
$EDITOR .env                       # set SNAP_IMAGE (a release tag) and SNAP_DOMAIN
sudo install -d -o 10001 -g 10001 -m 0700 data files secrets
docker compose up -d --wait
docker compose exec snap snap-server admin create-user --username alice --email alice@example.com --admin
```

The last command prints a link to set the password. Then open `https://<SNAP_DOMAIN>/` and sign in. The full walkthrough, with DNS, the raw origin, backups and upgrades, is [docs/deployment.md](docs/deployment.md); [docs/self-hosting.md](docs/self-hosting.md) is a guide to a complete setup. Every option is in [docs/configuration.md](docs/configuration.md), and the documentation index is [docs/README.md](docs/README.md).

To try snap on your own machine without DNS or certificates, `make demo-up` starts it on `127.0.0.1` ([docs/dev/demo.md](docs/dev/demo.md)).

## Usage

**Browser.** Sign in at `/`, drop files on the page or use the Paste, Link and Secret tabs. Manage everything at `/dashboard` ([uploads](docs/uploads.md), [dashboard](docs/dashboard.md), [secrets](docs/secrets.md)).

**curl.** Create an API key at **Settings › API keys**, then `curl -H "Authorization: Bearer $KEY" -T photo.png https://share.example.com/api/v1/upload/` returns the item as JSON; add `?format=url` for just the link ([api.md](docs/api.md)).

**ShareX.** **Settings › Integrations** downloads `.sxcu` files for images and files, text and short links, each with its own key ([sharex.md](docs/sharex.md)).

**sp.** Download the client from your server's `/dl/` (linux amd64 and arm64, macOS arm64, windows amd64; the install line is on **Settings › Integrations**), or with Go: `go install github.com/Battleroid/snap-share/cmd/sp@latest`. Then run `sp login https://share.example.com`, then `sp up`, `sp paste`, `sp short`, `sp secret` and `sp open` ([cli.md](docs/cli.md)).

## Security

Uploaded content is served with a sandboxing CSP and safe content types, so a file cannot run script on the app's origin, and it can be moved to a separate raw origin. One-time secrets are encrypted under a key in the link's fragment, which the server never receives: the browser trusts the JavaScript the server sends, `sp` holds even against a compromised server, and sealed (curl, ShareX) secrets are encrypted by the server itself. Account and deletion tokens travel in URL fragments, snap's logs never contain tokens, secret ids or query strings, and visitor IPs are shown only to the item's owner and deleted after the retention period; the threat model and stated limits are in [docs/security.md](docs/security.md).

## Development

Every tool runs in a pinned container through the Makefile, so Docker is all you need. How to propose a change is in [CONTRIBUTING.md](CONTRIBUTING.md); the rules for code, UI and commits are in [docs/dev/conventions.md](docs/dev/conventions.md).

| Command | Does |
|---|---|
| `make test` | Go tests (vet, race detector) and JS tests |
| `make lint`, `make vuln` | golangci-lint, govulncheck |
| `make build` | binaries into `bin/` |
| `make image` | the runtime image (`IMAGE=snap:dev`) |
| `make e2e-isolated` | Playwright against a private stack with no published ports |
| `make help` | every target |

CI (`.github/workflows/ci.yml`) runs the same checks through make and publishes images for `v*` tags.

## Licence

snap is released under the [MIT licence](LICENSE). Third-party files keep their own licences; they are listed in [internal/web/static/vendor/VENDOR.md](internal/web/static/vendor/VENDOR.md).
