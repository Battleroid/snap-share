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

Run the example deployment in `deploy/` (snap and Caddy with automatic HTTPS): [docs/deployment.md](docs/deployment.md). All options are in [docs/configuration.md](docs/configuration.md); the documentation index is [docs/README.md](docs/README.md).

## Usage

**Browser.** Sign in at `/`, drop files on the page or use the Paste, Link and Secret tabs. Manage everything at `/dashboard` ([uploads](docs/uploads.md), [dashboard](docs/dashboard.md), [secrets](docs/secrets.md)).

**curl.** Create an API key at **Settings › API keys**, then `curl -H "Authorization: Bearer $KEY" -T photo.png https://share.example.com/api/v1/upload/` returns the item as JSON; add `?format=url` for just the link ([api.md](docs/api.md)).

**ShareX.** **Settings › Integrations** downloads `.sxcu` files for images and files, text and short links, each with its own key ([sharex.md](docs/sharex.md)).

**sp.** Download the client from `/dl/` (listed on **Settings › Integrations**), run `sp login https://share.example.com`, then `sp up`, `sp paste`, `sp short`, `sp secret` and `sp open` ([cli.md](docs/cli.md)).

## Security

Uploaded content is served with a sandboxing CSP and safe content types, so a file cannot run script on the app's origin, and it can be moved to a separate raw origin. One-time secrets are encrypted under a key in the link's fragment, which the server never receives: the browser trusts the JavaScript the server sends, `sp` holds even against a compromised server, and sealed (curl, ShareX) secrets are encrypted by the server itself. Account and deletion tokens travel in URL fragments, snap's logs never contain tokens, secret ids or query strings, and visitor IPs are shown only to the item's owner and deleted after the retention period; the threat model and stated limits are in [docs/security.md](docs/security.md).

## Development

Every tool runs in a pinned container through the Makefile; nothing is installed on the host. Rules for code, UI and commits: [docs/dev/conventions.md](docs/dev/conventions.md).

| Command | Does |
|---|---|
| `make test` | Go tests (vet, race detector) and JS tests |
| `make lint`, `make vuln` | golangci-lint, govulncheck |
| `make build` | binaries into `bin/` |
| `make image` | the runtime image (`IMAGE=snap:dev`) |
| `make e2e-isolated` | Playwright against a private stack with no published ports |
| `make help` | every target |

CI (`.gitea/workflows/ci.yml`) runs the same checks and publishes images for `v*` tags.

## Licence

To be decided.
