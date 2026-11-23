# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

snap is a self-hosted sharing server: files, pastes, short links, albums and one-time secrets. It's one Go binary (`cmd/snap-server`) with SQLite, plus a CLI client (`cmd/sp`). The full rules for code, UI, docs and commits are in `docs/dev/conventions.md`. Read it before making non-trivial changes.

## Commands

Every toolchain runs in a pinned container through the Makefile. Nothing is installed on the host, so Docker is the only requirement. Caches live in `~/.cache/snap`.

```sh
make test                                   # go vet + go test -race ./..., then node tests in test/js
make test-go PKG=./internal/items/...       # one package (vet + race)
make go ARGS="test -run TestEventsTimeline ./internal/secrets/"   # one test (no -race)
make test-js                                # browser module tests (node:test)
make lint                                   # golangci-lint (.golangci.yml)
make vuln                                   # govulncheck
make fmt / make tidy                        # gofmt -s -w, go mod tidy
make go ARGS="get example.com/mod@v1.2.3"   # any go command; pin new deps this way
make build                                  # binaries into bin/
make image                                  # runtime image (IMAGE=snap:dev)
make e2e-isolated ARGS=specs/x.spec.mjs     # Playwright against a private stack, no published ports
make demo-up                                # LAN demo at https://203.0.113.5:8443, Mailpit at :8025
```

- Tests must not use the network or publish Docker ports.
- Regenerate the e2e golden vectors with `make go ARGS="test ./internal/e2e -run TestVectors -update"`.
- CI (`.github/workflows/ci.yml`) calls the same `make` targets, so it uses the same pinned toolchain.

## Architecture

**Wiring.** `internal/app` builds everything. `app.New` opens the DB, runs migrations and creates the blob stores. `routes.go` registers the feature packages, and the order matters:
- items comes before core, because the composer replaces core's home page;
- notify comes before secrets, which uses its notifier;
- admin comes last.

Each feature package exposes `New(Deps)` and `Register(*web.Router)`. Packages depend on each other through hooks set during wiring, not through each other's internals. For example, `items.Deps.OnCreate` feeds `mediajobs`, and `Items.SetAlbumPage` lets `albums` render album share pages.

**HTTP layer (`internal/web`).**
- **Access.** Every route must declare one: `web.Public()`, `web.SignedIn()` (browser sessions only, API keys are refused), `web.Scope(auth.ScopeX)` or `web.Admin()` (also checked against `SNAP_ADMIN_CIDRS`). Registering a route without one panics.
- **Body limits.** By default, bodies are capped at 1 MiB with a 30 s read deadline, and multipart is refused. Upload routes opt out with `web.LargeBody()` and enforce their own limits.
- **Other route options.** `web.Redact()` marks a path that carries a token or secret id (request logs carry only the route pattern, never the path). `web.WithCSP(...)` picks the CSP: `CSPWasm` for secret pages, `CSPNone` plus `web.SetRawHeaders` for user content.
- **Request middleware.** `http.CrossOriginProtection` and authentication run before the router. `RawHost` confines the optional raw origin (`SNAP_RAW_URL`) to content paths and strips cookies and auth there.
- **Errors.** `Errors.Error(w, r, status, "Sentence.")` renders JSON under `/api/` and an HTML page everywhere else.

**Principals (`internal/auth`).** A request acts as a session or as an API key. A key carries scopes and constraints (upload size, expiry, visibility, networks), which can only lower the site and user limits. `items.IsOwner` gives the owner's view only to the owner's session, or to one of their keys with `items:read`.

**Slug namespace.** `items` owns every path no route matches: it is the router's `Fallback`, which dispatches `/{slug}`, `/{slug}/raw|download|thumb|preview` and `/{slug}.{ext}`. A new top-level path segment must also be added to `reservedNames` in `internal/items/slug.go`, or a user's slug can shadow it.

**Storage.**
- SQLite with a single writer: writes go through `db.Tx` (`BEGIN IMMEDIATE` on `db.W`), and reads use `db.R`.
- Tables are STRICT, and timestamps are INTEGER unix milliseconds UTC (`db.Millis`, `db.Time`).
- Never hold a transaction or open rows across client I/O.
- Migrations live in `internal/db/migrations/NNNN_name.sql`, numbered by area: `01xx` core, `02xx` auth, `03xx` items, `04xx` keys, `05xx` secrets, `06xx` stats, `07xx` albums/dashboard, `08xx` admin.
- Blobs (`internal/blob/local`) have separate roots for item files and for secrets.

**Settings.** A setting is declared with `settings.Define("area.name", default, validator)` next to the code that reads it, and read with `X.Get(store)`. Admins change settings in Admin › Settings or with `snap-server admin settings set`. Environment config is in `internal/config`.

**Secrets.** There are two modes:
- **e2e:** the browser or `sp` encrypts under a key in the link fragment (`/s/{id}#key`), which the server never receives. The envelope spec is in `docs/security.md`. `internal/e2e` is the Go reference implementation and `internal/web/static/js/e2e.js` the browser one; both are tested against `internal/e2e/testdata/vectors.json`.
- **sealed:** curl and ShareX uploads, which the server encrypts itself.

Pages whose fragment carries a key or token load `js/fragment.js` as a classic script in `<head>`, which strips the fragment before any module runs.

**Background work.** `jobs.Queue.Handle(kind, …)` runs durable jobs and `Every(name, interval, fn)` runs sweepers. `mediajobs` makes thumbnails, posters and teasers with a sandboxed ffmpeg (`docs/media.md`).

**Observability.** `internal/obs` sets up the app log (one `request` record per request, with a request id and the route pattern) and the optional audit stream; `internal/metrics` serves Prometheus metrics on a separate listener, and packages count their events through it. Neither may carry a credential or a secret id (`internal/redteam` checks). See `docs/observability.md`.

**Frontend.**
- Templates live in `internal/web/templates/<feature>/`. Shared partials are in `_partials/`: Patchbay controls (`pb.*`) and the page patterns in `page.html` (`page.head`, `page.empty`, `row.actions`, `confirm`, `table.foot`, `media.tile`, …; `/_kit` shows them live). `_layout` holds the three frames (`app`, `site`, `auth`); `render.go`'s `pageLayouts` picks one per page and `NavKey` marks the sidebar entry from the URL.
- Render pages with `Render.Page(w, r, status, "feature/page", web.Page{...})` and htmx fragments with `Render.Fragment`.
- Browser JS is plain ES modules in `internal/web/static/js/`, with no build step.
- CSS is the vendored Patchbay kit (`static/vendor/patchbay/`, pinned with hashes in `static/vendor/VENDOR.md`; component docs in `docs/dev/patchbay/`). `app.css` is page layout only.
- No `style=` attributes and no `on*=` handlers in templates; a test enforces this.

## Tests that guard more than code

- `internal/app/docs_test.go` fails if `docs/configuration.md` doesn't document every env var `config.Load` reads and every registered setting. It also checks that relative doc links resolve, that `docs/README.md` indexes every doc, and that CI and `deploy/` agree with the toolchain pins and with each other. Update the docs in the same change.
- `internal/redteam` holds regression tests for past security findings, written as inverted PoCs. Keep them passing, and add one for each new finding.
- `testutil.NewServer(t)` starts a full TLS instance with fake auth (`s.Auth.Add`, `authtest.Session`, `authtest.Bearer`) and fake mail (`s.Mail.Sent()`).

## Conventions worth repeating

- Never log secrets, tokens, keys, Authorization headers, cookies or query strings. Use `crypto/subtle` for security-relevant comparisons, and `os.Root` for paths under user-influenced directories.
- Commit messages look like `area: what changed`, with no co-author or attribution lines.
- Docs are plain and short. UI copy uses sentence case for titles and buttons, lowercase for chrome, mono for values, `·` to join and `—` for empty. Helper text appears only where it prevents a mistake.
