# Development conventions

These rules apply to everyone who changes this repository. [CONTRIBUTING.md](../../CONTRIBUTING.md) covers how to propose a change.

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
| `make e2e-isolated ARGS=specs/x.spec.mjs` | build the image and run Playwright against a private stack with no published ports (safe to run beside another checkout) |
| `make e2e-isolated-media ARGS=specs/media.spec.mjs` | the same with ffmpeg in a media worker container (`deploy/e2e/compose.media.yml`); also checks that the worker made a poster (so ARGS must include a video spec) |
| `make demo-up`, `make e2e`, `make shots PAGES="/ /x"` | the [local demo](demo.md) on 127.0.0.1 (`DEMO_HOST=<your LAN IP>` to reach it from another machine) and runs against it |
| `make vendor-patchbay KIT=../patchbay-ui-kit REF=<commit>` | re-vendor the Patchbay kit: files, `VENDOR.md` hashes and `docs/dev/patchbay` (`DIST=<build dir>` takes the files from a kit build instead of the commit) |

The dev image (`docker/dev.Dockerfile`) is Go 1.27.4 on Alpine 3.24 with ffmpeg 8.1, golangci-lint and govulncheck. The module and build caches live in `~/.cache/snap` and are shared between checkouts and worktrees.

Tests must not use the network. Tests must not publish Docker ports.

## Layout and ownership

- Module: `github.com/Battleroid/snap-share`. Binaries: `cmd/snap-server`, `cmd/sp`.
- `internal/<pkg>` per concern: one package per feature or subsystem, wired together in `internal/app`. Keep a change within the packages it concerns; a change that needs another package's internals usually wants a hook (`Deps`) instead.
- Migrations: `internal/db/migrations/NNNN_name.sql`, numbered by area:
  `01xx` core, `02xx` auth, `03xx` items, `04xx` keys, `05xx` secrets, `06xx` stats, `07xx` albums/dashboard, `08xx` admin.
- Routes: a feature package exposes `New(Deps)` and `Register(r *web.Router)`. Wire it in `internal/app/routes.go` (and fields in `internal/app/app.go` if needed) in a block of its own, and change nothing else there. The order matters ([CLAUDE.md](../../CLAUDE.md#architecture)).
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
- Add a dependency only when the standard library will not do. Pin it with `make go ARGS="get mod@version"` and say why in the pull request.
- No package-level mutable state; pass dependencies explicitly. `context.Context` first on anything that does I/O.
- Errors: wrap with `%w` and lowercase messages; sentinel errors for conditions callers branch on.
- Tests: table-driven, in the same package unless testing the public API (`_test` package). Golden files in `testdata/`, regenerated with `go test ./pkg -update`.
- Never log secrets, tokens, keys, Authorization headers, cookies or query strings.
- Security-relevant comparisons use `crypto/subtle`.

## UI

The kit is Patchbay, soft edge, vendored in `internal/web/static/vendor/patchbay/` (pinned with hashes in `static/vendor/VENDOR.md`). How snap loads it is in [the Patchbay README](patchbay/README.md#using-these-files); component markup and rules are in `docs/dev/patchbay/components/<Name>/README.md` and `example.html`.

**Frames and pages.**
- `render.go`'s `pageLayouts` picks the frame: `app` (AppShell: header with the quota ring, bell and account menu; the sidebar), `site` (SiteHeader: share pages and albums) or `auth` (one centered card: sign-in, unlock, secret open, errors). Pages never pick one.
- Build every page from the partials in `_partials/page.html`, each documented above its definition, and see them live at `/_kit`:
  - `page.head` for the title, meta line and actions; the primary action goes last.
  - `page.empty` / `page.emptyrow`: "No X yet", one sentence, one action.
  - `page.dl` for read-only pairs.
  - `table.foot` (count and pager) under every table.
  - `row.actions` for one inline icon action plus an overflow menu.
  - `confirm` / `data-confirm-*` instead of per-row dialogs; `dialog.head` / `dialog.foot` for the rest.
  - `media.tile` for the dashboard grid and the album gallery.
  - `page.result` for gone, expired, not-found and no-access states.
- `templates/keys/list.html` is the reference page.
- Settings-type forms are `form[data-section]` with `pb.footstatus` in `.pb-fieldset__foot`. `.pb-form__actions` is only for one-shot forms.
- Form widths: `.app-form-col` for settings-type and create forms. The other widths are the page, the `pb-auth` card and SettingsPage's main column.

**Kit elements.** Every control and content type uses its kit element; `TestTemplatesUseKitControls` enforces it, and markup built in JS follows the same rules.
- Inputs are `.pb-input`. Dates and times are DatePicker/TimePicker and colours are ColorPicker, never native `type=date|time|color`. Selects are `.pb-select`. Choices are Checkbox (form option), Switch (applies at once), Radio (2–6 options), Segmented (toolbar toggle) or Select (more than six).
- Video and audio are MediaPlayer (`figure.pb-player`), never native `controls`. The exceptions are the scriptless `/{slug}/player` and the muted hover teasers.
- Images open in ImageViewer. Code and text use the read-only CodeArea, rendered Markdown uses Prose.
- Tables are `table.pb-table` in `pb-tablewrap`. Records are ListItem or DetailList, and read-only figures are StatTiles in `pb-dash`, never settings rows.
- Feedback: Notice for a problem on the page, Toast for the result of the user's action, Notifications for what happened while away, `pbConfirm` to confirm.
- Times are `{{template "pb.time" .T}}`: the kit's relative time, with the absolute UTC time as its title and as the text without script. Don't format dates by hand in lists, tables, timelines or meta lines.

**Tables.**
- No horizontal scroll at 1280 px. Merge related data into one cell (avatar, name and a "you" badge, with the email as a muted second line).
- Names, sizes and dates never wrap; long text truncates and carries a `title`. Rows keep one height.
- Search fills the toolbar. Toolbar controls are the small size (`pb-input--sm`, `pb-select--sm`, `pb.daterange` with `"sm" true`).
- Under 640 px (40rem), low-priority columns hide (`.app-hide-sm`); their data stays reachable through the row menu or the detail page.
- No-match and empty states are a compact `page.emptyrow` with an action.

**Badges.**
- Badges mark exceptions only, such as "admin", "disabled" or "expired". Normal states get no badge, and a disabled or revoked row is muted (`.app-row-muted`).
- Who can open an item is `item.visibility`, for the owner only, in muted icon + word rather than a status colour. Everywhere but the composer's Recent list only `private` and the yellow `password` badge show, since unlisted is the default: pass `"exceptions" true`, or leave the visibility empty for an unlisted item.
- An item's own link is `item.share` (Copy link and a QR popover; `showLink` adds the link text and Open where it isn't on screen). CopyField (`pb.copy`) is only for one-time values the user can't get again: a new API key, recovery codes, a secret link at creation, the TOTP secret.

**CSS and scripts.**
- Use tokens and `pb-` classes. `static/css/app.css` is page layout only (`TestAppCSSIsLayoutOnly`: no `.pb-` selectors, no raw px but 1px and 2px, no hex). There is no other app stylesheet.
- No `style=` attributes, `on*=` handlers or inline `<script>` in templates; a test enforces it. The one exception is a data block the kit reads, `<script type="application/json">` with a single template action as its body (the stats time-series chart, `stats.chart`); it never runs. Set custom properties in templates with `data-vars="--value:42%"`, and from JS with `el.style.setProperty`.

**Copy.**
- Sentence case for titles and buttons, lowercase for chrome (labels, pills, badges), mono for values (hosts, sizes, dates, IDs), `·` to join, `—` for empty. No emoji.
- Helper text only where it prevents a mistake. No restating what a control already says.

## Docs

Plain and short. State what something does and how to use it; no marketing language, no filler. A change that adds or alters behaviour updates the doc for it (`docs/<topic>.md`) in the same change.

## Commits

Keep commits small and each one coherent. Messages: `area: what changed` (e.g. `e2e: add STREAM chunk encryption`). No co-author or attribution lines.
