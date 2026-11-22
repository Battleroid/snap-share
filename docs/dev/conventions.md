# Development conventions

These rules apply to everyone working in this repo, including parallel agents on track branches.

## Toolchain

Nothing is installed on the host. Every tool runs in a container through the Makefile:

| Command | Does |
|---|---|
| `make test-go PKG=./internal/e2e/...` | `go vet` + `go test -race` (default `PKG=./...`) |
| `make test-js` | `node --test test/js/` in `node:24.22.3-alpine3.24` |
| `make lint` | golangci-lint (`.golangci.yml`) |
| `make vuln` | govulncheck |
| `make fmt`, `make tidy` | gofmt, go mod tidy |
| `make go ARGS="get example.com/mod@v1.2.3"` | any go command |
| `make build`, `make image` | binaries into `bin/`, runtime image |
| `make e2e-isolated ARGS=specs/x.spec.mjs` | build the image and run Playwright against a private stack with no published ports (safe in parallel) |
| `make demo-up`, `make e2e`, `make shots PAGES="/ /x"` | integrator only: the LAN demo on 203.0.113.5 and runs against it |

The dev image (`docker/dev.Dockerfile`) is Go 1.27.4 on Alpine 3.24 with ffmpeg 8.1, golangci-lint and govulncheck. The module and build caches live in `~/.cache/snap` and are shared between worktrees.

Tests must not use the network. Tests must not publish Docker ports.

## Layout and ownership

- Module: `github.com/Battleroid/snap-share`. Binaries: `cmd/snap-server`, `cmd/sp`.
- `internal/<pkg>` per concern; see the plan's repo layout. Touch only the paths your track owns. If you need a change elsewhere, stop and report it.
- Migrations: `internal/db/migrations/NNNN_name.sql`, numbered in your track's range:
  `01xx` core, `02xx` auth, `03xx` items, `04xx` keys, `05xx` secrets, `06xx` stats, `07xx` albums/dashboard, `08xx` admin.
- Routes: a feature package exposes `New(Deps)` and `Register(r *web.Router)`. Wire it in `internal/app/routes.go` (and fields in `internal/app/app.go` if needed) by adding lines in a block commented with your track name; change nothing else there. The integrator resolves the one-line merge conflicts.
- Every route declares access: `web.Public()`, `web.SignedIn()`, `web.Scope(auth.ScopeX)` or `web.Admin()`; registering without one panics. Routes whose path carries a token or secret id also use `web.Redact()`. Secret pages use `web.WithCSP(web.CSPWasm)`; handlers serving user content use `web.WithCSP(web.CSPNone)` and `web.SetRawHeaders`.
- Errors: `a.Errors.Error(w, r, status, "Sentence with a full stop.")` renders JSON for `/api/` and a page otherwise. API success bodies use `web.JSON`; curl-friendly endpoints honour `web.WantsURL`.
- Pages: `Render.Page(w, r, status, "feature/page", web.Page{Title, Nav, Scripts, Data})`. Page templates define `content` (and optionally `head`). htmx fragments: `Render.Fragment(..., "block-name", ...)`.
- Tests: `testutil.NewServer(t)` starts a full TLS instance with fake auth (`s.Auth.Add`, `authtest.Session(req, name)`, `authtest.Bearer`) and fake mail (`s.Mail.Sent()`). Requests from `s.Request` carry same-origin headers.
- Timestamps in the schema are INTEGER unix milliseconds UTC (`db.Millis`, `db.Time`); tables are STRICT. Writes go through `db.Tx` (BEGIN IMMEDIATE on the single writer); reads use `db.R`. Never hold a transaction or open rows across client I/O.
- Settings: `var X = settings.Define("area.name", default, validator)` next to the code that reads it; `X.Get(store)`.
- Background work: `jobs.Queue.Handle(kind, concurrency, timeout, fn)` for durable jobs, `Every(name, interval, fn)` for sweepers.
- Templates: `internal/web/templates/<feature>/`. Shared Patchbay partials live in `internal/web/templates/_partials/`; use them instead of copying markup.
- Browser JS: `internal/web/static/js/<feature>.js`, ES modules, no build step. Third-party files go in `internal/web/static/vendor/<name>/` with the version and source recorded in `internal/web/static/vendor/VENDOR.md`.
- Browser JS tests: `test/js/*.test.mjs` (node:test, WebCrypto via `globalThis.crypto`).

## Go

- Go 1.27 stdlib first: `net/http` patterns, `log/slog`, `crypto/hkdf`, `os.Root` for any path under a user-influenced directory.
- New dependencies only if your brief allows them. Pin with `make go ARGS="get mod@version"` and list them in your report.
- No package-level mutable state; pass dependencies explicitly. `context.Context` first on anything that does I/O.
- Errors: wrap with `%w` and lowercase messages; sentinel errors for conditions callers branch on.
- Tests: table-driven, in the same package unless testing the public API (`_test` package). Golden files in `testdata/`, regenerated with `go test ./pkg -update`.
- Never log secrets, tokens, keys, Authorization headers, cookies or query strings.
- Security-relevant comparisons use `crypto/subtle`.

## UI

- Patchbay, soft edge: `internal/web/static/patchbay/patchbay.css`. Component markup and rules: `docs/dev/patchbay/components/<Name>/README.md` and `example.html`.
- Use tokens and `pb-` classes only. App CSS is page layout only.
- No `style=` attributes and no `on*=` handlers in templates; a test enforces it. Set CSS custom properties (e.g. `--value`) from JS with `el.style.setProperty`.
- Copy: sentence case for titles and buttons, lowercase for chrome (labels, pills, badges), mono for values (hosts, sizes, dates, IDs), `·` to join, `—` for empty. No emoji.
- Helper text only where it prevents a mistake. No restating what a control already says.

## Docs

Plain and short. State what something does and how to use it; no marketing language, no filler. Each track writes the docs for what it adds (`docs/<topic>.md`).

## Commits

Commit on your track branch only. Messages: `area: what changed` (e.g. `e2e: add STREAM chunk encryption`). No co-author or attribution lines.
