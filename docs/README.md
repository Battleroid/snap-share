# Documentation

## Running snap

- [deployment.md](deployment.md): the example deployment with Caddy, first run, backups, upgrades.
- [configuration.md](configuration.md): every environment variable and admin setting.
- [gitea-sso.md](gitea-sso.md): sign-in with Gitea (OIDC).
- [observability.md](observability.md): the app log, the audit stream for log shippers, Prometheus metrics and their cost.
- [security.md](security.md): the E2E envelope format, what the server stores and sees, the threat model and stated limits.

## Using snap

- [accounts.md](accounts.md): sign-up modes, invites, email verification, sessions, SSO accounts, the admin CLI.
- [uploads.md](uploads.md): files, pastes and short links: limits, names, protection, expiry, how content is served.
- [dashboard.md](dashboard.md): finding and changing items in bulk, Send link, albums.
- [secrets.md](secrets.md): one-time secrets in the browser, with `sp`, and with curl.
- [stats.md](stats.md): what is recorded when someone opens a link, and for how long.
- [sharex.md](sharex.md): ShareX uploader files.
- [cli.md](cli.md): the `sp` command line client.
- [notifications.md](notifications.md): optional email and ntfy messages when something is opened.
- [api.md](api.md): the HTTP API.

## Administering snap

- [admin.md](admin.md): the admin area: users, invites, content, secrets, settings, audit log.

## Working on snap

- [dev/conventions.md](dev/conventions.md): toolchain, layout, code and UI rules.
- [dev/demo.md](dev/demo.md): the local demo, its hostname and certificate.
- [media.md](media.md): thumbnails, posters, teasers and how ffmpeg is constrained.
