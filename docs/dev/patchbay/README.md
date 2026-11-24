# Patchbay

## Using these files

These are the kit's docs at the commit snap vendors (`0110287` on `main` of https://github.com/Battleroid/patchbay-ui-kit): this README after this section, `CHANGELOG.md`, and `components/<Name>/README.md` plus `example.html` for every component. `make vendor-patchbay KIT=<kit clone> REF=<commit>` copies them again with the vendored files, keeps this section, and rewrites the hashes in `VENDOR.md`; `/_kit` names the commit from there. The kit's `demo.html`, `patchbay.js`, `icons/`, `app-icons/`, `tools/` and `tests/` are not copied; they live in the kit repo.

- **Files.** `internal/web/static/vendor/patchbay/` holds `patchbay.css`, `patchbay.mjs`, `pb-theme-boot.js`, `tokens.json`, `fonts/` and `LICENSE`, byte for byte from that commit. `static/vendor/VENDOR.md` records the commit and a sha256 per file, and `TestVendoredPatchbayHashes` checks them. Update a file, its hash and these docs together. Icons are data URIs inside the CSS.
- **Head.** `_layout/base.html` loads `pb-theme-boot.js` as a plain blocking script before `patchbay.css`, so the saved theme applies before the first paint, then `css/app.css`. `<html data-edge="soft">` and `<body class="pb">` are set there, for all three frames (`layout.app`, `layout.site`, `layout.auth`).
- **Behaviours.** `js/app.js` (a module on every page) imports `init` from `js/kit.js`, which calls the kit's `pbInit(root, {skip: SKIP})` from `patchbay.mjs`. It runs on load and on every `htmx:load`; scripts that insert markup call `init(el)` too. `pbInit` binds each element once, so a second call does nothing. `app.js` also imports `pbToast` and `pbConfirm` (flash messages, `data-confirm-*` buttons).
- **Skipped hooks.** `kit.js` `SKIP` lists the hooks snap does not take from the kit, with the reason next to each:
  - `strength`: snap scores passwords itself (`app.js`).
  - `table`: tables sort, select and page on the server (`dashboard.js`, admin).
  - `inbox`: `inbox.js` binds the bell and the notifications page with the server's unread count.
  - `codearea`: `composer.js` keeps the editable CodeArea in step itself.
- **Form sections.** Settings-type forms are `form[data-section]` with `{{template "pb.footstatus"}}` in `.pb-fieldset__foot`. The kit's `section` hook tracks No changes / Unsaved changes against a snapshot and settles on `pb:saved` / `pb:error`. One-shot forms use `.pb-form__actions` instead.
- **Custom properties.** Templates set them with `data-vars="--value:42%"` (the kit's `vars` hook), never `style=`. snap's own scripts set them with `el.style.setProperty`, which the CSP allows.
- **No style attributes.** The CSP allows styles only from `/static/`. `TestTemplatesHaveNoInlineStyleOrHandlers` rejects `style=` and `on*=` in templates, and `TestStaticJSHasNoEval` rejects `eval`, `new Function` and, in the vendored kit, `style="…"` in markup strings. Markup the kit builds as a string carries `data-style`, which `pbStyled(container)` moves into the CSSOM; snap's scripts build elements instead of HTML strings with styles.
- **Kit controls everywhere.** `TestTemplatesUseKitControls` fails on a native date, time or colour input, a `<select>`, text input, checkbox, table, dialog or button without its kit class, a `<video>`/`<audio>` with `controls` or outside `figure.pb-player` (except `share/player` and the muted hover teasers), and `<pre>` code outside `.pb-code` or `.pb-prose`.
- **Page partials.** `internal/web/templates/_partials/page.html` holds the page patterns (`page.head`, `page.empty`, `page.emptyrow`, `page.dl`, `page.result`, `dialog.head`, `dialog.foot`, `confirm`, `table.foot`, `row.actions`, `media.tile`, `item.visibility`, `item.share`), each documented in a comment above its definition. `_partials/pb.html` wraps single kit controls (`pb.icon`, `pb.badge`, `pb.time`, `pb.field`, `pb.select`, `pb.copy`, `pb.date`, `pb.tags`, `pb.pager`, …). Build pages from these rather than copying kit markup.
- **Gallery.** `/_kit` (admins, `core/kit.html`) shows every partial live with the vendored commit named at the top. Check a change to a partial there.
- **App CSS.** `static/css/app.css` is page layout only, on tokens: `TestAppCSSIsLayoutOnly` rejects `.pb-` selectors, raw px other than 1px and 2px, and hex colours. Give the element an `app-` class instead of restyling a kit class.

The rest of this file is the kit's own README.

**Demo:** [battleroid.github.io/patchbay-ui-kit](https://battleroid.github.io/patchbay-ui-kit/) (or open `demo.html` locally).

## Setup

- **CSS:** link `patchbay.css` (tokens, fonts, every component). Fonts load from `fonts/` beside it; keep the two together. There is no `tokens.css` or `components/bundle.css` any more; it is all in `patchbay.css`.
- **Theme without a flash:** put `<script src="pb-theme-boot.js"></script>` in `<head>` before the stylesheet (a plain blocking script, not `defer` or a module). It applies the saved choice (`localStorage` `pb-theme`: `light`, `dark` or `system`), else `<html data-theme-default="light">`, else the system setting, as `data-theme` and `data-theme-choice` on `<html>`.
- **Root:** `class="pb"` on the app root (the body margin is reset there), `data-edge="soft"` or `data-edge="hard"` on `<html>`.
- **Behaviors, classic script:** `<script src="patchbay.js" data-pb-auto></script>` at the end of `<body>` runs `pbInit()` on DOMContentLoaded. Without `data-pb-auto` nothing runs until you call `pbInit()`.
- **Behaviors, ES module:** `import pbInit, { pbToast, pbConfirm } from './patchbay.mjs'; pbInit();`. Exports: `pbInit` (also the default), `pbConfirm`, `pbToast`, `pbInbox`, `pbTheme`, `pbPlace`, `pbCalendar`, `pbClock`, `pbQRSvg`, `pbQRCode`, `pbVirtual`, `pbAnsi`, `pbCronParse`, `pbSection`, `pbHook`, `pbEsc`, `pbStyled`.
- **`pbInit(root = document, {only, skip} = {})`** binds every hook under `root` (and `root` itself) once per element, so it is safe to call again after an htmx swap or for markup you insert: `pbInit(el)`. `only` and `skip` take hook names; `data-pb-skip="name other"` on an ancestor skips those hooks below it. One element that throws logs `[patchbay] name` and the rest still bind. `pbInit.hooks` lists the names in order: `vars`, `toasts`, `kbd`, `tip`, `progress`, `datefield`, `timefield`, `combo`, `tags`, `codeinput`, `swatches`, `colorfield`, `copy`, `reveal`, `kv`, `range2`, `toggle`, `indeterminate`, `count`, `stepper`, `colortext`, `strength`, `range`, `tabs`, `section`, `open`, `inbox`, `table`, `player`, `codearea`, `shell`, `sitehead`, `trange`, `refresh`, `ts`, `share`, `fpreview`, `dt`, `evcal`, `md`, `thread`, `notify`, `dropset`, `sheet`, `session`, `themeswitch`, `popover`, `hovercard`, `menu`, `tree`, `cmd`, `contextmenu`, `imageviewer`, `avatar`, `pager`, `validate`, `reltime`, `midtrunc`, `clamp`, `qr`, `resizable`, `log`, `logx`, `diff`, `files`, `term`, `cron`, `storage`, `rack`, `heat`, `gauge`, `gaugedonut`, `bars`, `hist`, `json`, `bulk`, `qf`, `sortable`, `keys`, `upq`, `sectionnav`, `dismiss`, `confirm`, `autosubmit`, `teaser`, `sharelink`, `ring`. Register your own with `pbHook(name, selector, fn)` before calling `pbInit()`.
- **Strict CSP, no inline style needed:** the kit runs under `default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; font-src 'self'` (add `media-src 'self' blob:` for the media player and its captions). It never uses `eval`, `new Function`, inline handlers or `style` attributes. Markup it builds as a string carries styles in `data-style`, which `pbStyled(container)` moves into the CSSOM right after insertion; call it after inserting your own strings too. In your markup, set custom properties with `data-vars="--w:42%;--c:var(--chart-2)"` (names `--a-z0-9`, values without `;`, `url(` or `expression`), and use the attribute forms for layout: `.pb-dash > [data-span="1..12"]`, `.pb-steps[data-n]`, `.pb-tabbar[data-n]`, `.pb-qr[data-size=sm|md|lg]`, `.pb-auth--inline`, `.pb-progress[data-value]`. `tests/fixtures/csp.html` is every component under that policy.
- **Script helpers:** `pbToast({title, body, bodyHTML, …})` and `pbConfirm({title, body, bodyHTML, typeToConfirm, …})` treat `body` as text and escape it; `bodyHTML` is the explicit opt-in for markup you trust; a DOM Node as `body` is appended. `pbEsc(s)` escapes `& < > " '`.
- Start every app from `components/AppShell` (header, nav, page header, footer, skip link).
- Before building a part, read `components/<Name>/README.md` for its markup and rules, and copy the markup from `components/<Name>/example.html`.
- Each component's README names its markup hooks (`data-cal`, `data-clock`, `data-combo`, `data-tags`, `data-codeinput`, `data-copy`, `data-kv`, …). `components/<Name>/example.html` files are self-contained: each carries its own copy of the script it needs, built from the same sources as `patchbay.js`, plus example-only glue (fake saves, sample data).
- Form sections keep their Save/Discard buttons inside the panel (see `components/Fieldset`).
- Tokens: `tokens.json` is the source; every value is also a CSS custom property (`var(--accent)`, `var(--space-4)`, `var(--radius-md)`). Use tokens, never raw hex or px.
- Icons: `<span class="pb-icon pb-icon--NAME"></span>` (paints in `currentColor`). Raw SVGs are in `icons/soft` and `icons/hard`.
- `demo.html` shows every component on one page; `index.html` opens it, so GitHub Pages serves the demo at the site root.
- Density: `data-density="compact"` on `<html>` or a container for dense views.
- App icons and a web manifest are in `app-icons/`; the alert email template is `email/alert.html`.
- `tools/build.sh` rebuilds everything from `tools/src` (see `tools/README.md`). `npm test` runs the accessibility, layout and screenshot checks against `demo.html`.
- License: MIT for the code and docs (`LICENSE`); the fonts are under the SIL Open Font License 1.1 (`fonts/OFL.txt`).
- Components: AlertEmail, ApiTokens, AppIcons, AppShell, AutoSubmit, Avatar, Badge, BarChart, Breadcrumbs, BulkActions, BusyRegion, Button, ButtonGroup, Card, Chart, ChartGround, Checkbox, ChoiceCard, CodeArea, CodeInput, ColorPicker, Combobox, CommandPalette, CommentThread, ConfirmAction, ContextMenu, CopyField, CronBuilder, Dashboard, DataTable, DatePicker, Density, DetailList, Diagram, Dialog, DiffViewer, Disclosure, DropZone, DualRange, EmptyState, ErrorSummary, EventCalendar, Fieldset, FileBrowser, FilePreview, Flow, Gauge, Heatmap, Histogram, Icons, ImageViewer, InputTypes, JsonViewer, KeyValue, Link, ListItem, Loading, LogExplorer, LogViewer, MarkdownEditor, Media, MediaList, MediaPlayer, MediaState, Menu, MobileNav, Notice, Notifications, NotifySettings, Pagination, Pill, Popover, PrintReport, Progress, Prose, QRCode, QueryFilter, Rack, Radio, Range, Resizable, Scrollbars, Segmented, Select, SessionTimeout, SettingsPage, ShareDialog, ShareLink, Shortcuts, SignIn, SiteBanner, SiteHeader, SkipLink, Sortable, Sparkline, StatTile, StatusPage, Steps, StorageBar, StorageRing, Switch, TableTools, Tabs, TagInput, Terminal, TextInput, TextUtils, ThemeSwitch, TimePicker, TimeRange, Timeline, Toast, Tooltip, TreeView, UploadQueue, VirtualList.

A document-first system for small self-hosted apps: status pages, dashboards, file and media sharing, config editors. Text is set like a well-kept personal site — a comfortable serif on warm paper, mono for anything a machine would print — with a few saturated pop fills for color. It ships in two edge variants that share every color and type token: **soft** (rounded, diffuse shadows) and **hard** (square, ink borders, offset shadows), and in two themes: **light** (warm paper, the default) and **dark** (warm charcoal).

## Setup

- Load `patchbay.css` (tokens, fonts and every component) and, in `<head>`, `pb-theme-boot.js`. Put `class="pb"` on the app root.
- Behaviors come from `patchbay.js` (classic script; add `data-pb-auto` to run `pbInit()` on DOMContentLoaded) or `patchbay.mjs` (ES module; `import pbInit from './patchbay.mjs'`). Both need no inline script or style, so they work under `script-src 'self'; style-src 'self'`.
- Pick a variant with `data-edge="soft"` (default) or `data-edge="hard"` on `<html>` or any container. Use one variant per app; do not mix them on a page.
- Pick a theme with `data-theme="light"` or `data-theme="dark"` on `<html>` (whole app) or any container (one panel). Default to the system setting with a ThemeSwitch in the header tools; `pb-theme-boot.js` in `<head>` applies the saved choice before the CSS paints (see ThemeSwitch).
- Start every app from AppShell: skip link, header (brand, ⌘K search, tools), side nav, page header, main, footer.
- Class prefix is `pb-`. Previews show both variants side by side. Behavior is optional: the markup and CSS work without script, and `patchbay.js` adds the interactive parts.
- Dense views (log tables, admin lists) set `data-density="compact"` on `<html>` or a container: 28px controls, tighter rows and cards. See Density.
- Windows high contrast (`forced-colors: active`) is handled in the bundle: borders, focus rings, selection and chart series stay visible. Never carry meaning in a background color alone.
- Every app ships the AppIcons head snippet (favicon, touch icon, manifest, `theme-color`).

## Content

- Write plainly and briefly. Sentence case for titles and buttons ("Copy link", "Recent incidents").
- Lowercase for UI chrome: meta labels, pill text, badge words (`up`, `degraded`, `expires`). Real names keep their casing (`Jellyfin`, `Home Assistant`).
- Hosts, ports, paths, IDs, sizes and times are mono and shown in full: `nas-01.lan:5001`, `2026-11-21`, `4.1 GB`.
- Join a label and its scope with `·` (`uptime · 30d`). Use `—` for a missing value. Units follow the number, smaller, in `ink-muted`.
- Incident and helper text says what happened and what to do: "Port must be 1–65535.", "Raised maxmemory; watching response times."
- No emoji. State is always icon + word + color.

## Color

- Ground is `bg` (warm paper). Cards, inputs and buttons sit on `surface` (white). `surface-sunk` fills hover rows, kbd, inline code, meter tracks. Code sits on `code-bg`.
- Text: `ink` for content and values, `ink-muted` for hosts, captions, helper text and axis labels. Both pass 6:1 on every ground in both themes.
- `accent` is the one interactive color: links, the primary button, the selected pill, the focus ring, chart-1. `accent-tint` marks selection and drag-over.
- Pop fills — `pop-yellow`, `pop-mint`, `pop-red` — carry color: card bands, the status banner, solid badges, pop pills, diagram roles, image placeholders. Text on them is `on-pop` (ink). Use at most two pops in one view.
- Status colors mean state only: `ok`, `warn`, `down`, `info`, `unknown`, each with a `*-tint` ground for badges. Never use them for decoration. `ok` and `down` differ by hue, so the icon and word are required.
- Code: `syn-keyword`, `syn-string`, `syn-number`, `syn-function`, `syn-type`, `syn-comment` (italic); all read on `code-bg` and `surface`.
- Dark theme: every color token has a dark value with the same role and the same contrast rules (text 4.5:1, marks 3:1); accent and status colors are lightened, tints darkened, `on-accent` turns dark. Never hard-code a hex; a raw color breaks one of the themes.
- Terminal output uses the `term-*` colors (red, green, yellow, blue, magenta, cyan), the same in both themes and 7:1 or better on `night`.
- `night` and `on-night` are the same in both themes: video stages, the image viewer, ink tiles and ink toasts. QR codes are always dark on white.
- Charts: `chart-1` (accent), `chart-2` (vermilion), `chart-3` (dashed baseline), `chart-4` (ochre); `chart-grid` for rules. Four series at most.

## Type

- `serif` (Source Serif 4, falling back to Times New Roman) for titles, prose, card titles, service and node names, helper text.
- `mono` (Obel Mono) for controls (`ui`), code (`code`), labels (`label`, uppercase, tracked), and every number (`metric`, `metric-xl`), with tabular figures.
- Prose is `body` (17/27) at `size-measure` (68ch). Dense UI copy is `body-sm`. Captions and help are `note` (italic).
- One `display` or `metric-xl` per page.

## Shape and depth

- Soft: `radius-lg` for cards, panels, drop zones; `radius-md` for buttons, inputs, code, nodes; `radius-sm` for badges and kbd; `radius-pill` for pills. Borders `line` (cards) and `line-strong` (controls). Shadows `shadow-soft-sm/md/lg`.
- Hard: `radius-0` everywhere. Borders are `ink`. Shadows are offsets: `shadow-hard-sm` (2px) on controls, `shadow-hard-md` (4px) on cards, `shadow-hard-lg` (6px) on popovers and active drop zones. Buttons press into their shadow on `:active`.
- Both: `shadow-inset` on fields; `stroke-1` borders; `stroke-2` focus ring in `accent`, 2px offset, on every focusable element.
- Rules and dividers are `line` hairlines; in hard they turn `ink`.

## Controls

- Build on native elements and style them with `pb-` classes: `pb-check`, `pb-radio`, `pb-switch`, `.pb-select`, `.pb-input` (every input type), `pb-range`, `pb-progressbar`, `pb-meterbar`, `pb-fieldset`, `pb-details`, `pb-dialog`, `[popover].pb-menu`, `table.pb-table`. Keyboard, form and accessibility behavior comes from the browser.
- Dates and times: use DatePicker and TimePicker; the browser's own date, time and color popups cannot be styled. Colors: ColorPicker.
- Long lists: Combobox (one value, searchable), TagInput (many values). Codes: CodeInput. Values to copy: CopyField. Env vars and headers: KeyValue. Min–max: DualRange. Options with detail: ChoiceCard.
- Pick the control by intent: Checkbox for independent options in a form, Switch for settings that apply at once, Radio for 2–6 exclusive options with hints, Segmented for short exclusive choices in a toolbar, Select for more than six.
- Conjoined controls: `pb-seg` for toggles (radios or checkboxes), `pb-btngroup` for related actions, split buttons and pagers, `pb-toolbar` for a row of mixed controls.
- Labels on every field (`pb-field__label`, uppercase mono). Help and errors go under the field in `note` style; errors in `down` with `status-down`.
- Video and audio: always MediaPlayer, never the browser's default controls.
- Navigation and finding: AppShell nav, Breadcrumbs, CommandPalette (⌘K), Pagination, TreeView for nested storage. Context menus duplicate actions found elsewhere; never hide an action only there.
- Lists and records: ListItem rows, DetailList for one thing's fields, Data table for comparing many, Timeline for the record of events. Empty and failed states use EmptyState and Result; loading uses skeletons or a spinner (Loading).
- Forms with more than two fields get an ErrorSummary at the top. Lists people filter often get a QueryFilter (`status:down host:nas-*`); selected rows get a BulkActions bar.
- Homelab views: LogViewer for live logs, Terminal for command output, DiffViewer for config changes, FileBrowser for storage, CronBuilder for schedules, StorageBar for pools, Rack for hardware.
- Time-based views put a TimeRange picker and auto-refresh in the page header; the time-series chart zooms by drag and follows the picker.
- Charts: Chart for lines over time, BarChart for amounts per category (stacked or horizontal), Histogram for distributions, Gauge for one value against limits, donuts for parts of a whole (five at most), Heatmap for per-day activity or pass/fail, JsonViewer for structured data.
- Over about 2,000 rows use VirtualList. Reorderable things use Sortable, never drag without a keyboard path. Uploads report progress in an UploadQueue. Every app lists its shortcuts in a Shortcuts sheet on `?`. Big tables use TableTools (columns, density, expandable rows, inline edit, CSV). Scheduled work shows in an EventCalendar.
- Feedback: Toast for the result of the user's own action (with Undo where possible), Notice for a problem on the page, Notifications for what happened while they were away, Dialog only to confirm or to collect input that cannot wait. SessionTimeout warns a minute before sign-out. Alerts sent by email use the AlertEmail template.
- Panels people pick or open are `pb-card--outline`: a quiet outline that turns `accent` on hover and focus.

## Layout

- 4px grid: `space-1` … `space-12`. Card padding `space-6`, gaps between cards `space-6`, between sections `space-8`.
- Pages max `size-page` (1120px), centered, with `space-8` gutters. Prose columns max `size-measure`.
- Control height `size-control` (36px), small `size-control-sm` (28px).
- Apps: AppShell with a 240px nav; under 900px the nav becomes a drawer. Page content uses `pb-pagehead` then `pb-layout` (content plus a 300px aside, stacking when narrow). Resizable splits list and preview panes.
- Site banners sit above the shell header, one at a time.
- Public sites and docs use SiteHeader (brand, links or dropdown menus, search, theme, one call to action) instead of AppShell; under 760px the links fold into a menu. Phone-first apps use MobileNav: a bottom tab bar (five tabs at most) and action sheets.
- Page templates: Dashboard (12-column `pb-dash` grid), SettingsPage (section nav, setting rows, danger zone last), SignIn (sign-in, two-factor setup, code prompt), ApiTokens (show once, revoke). Start new screens from these.
- Scrollbars are thin and themed automatically inside `.pb`.
- Printing hides app chrome, drops shadows and expands scroll areas; reports use `pb-report` (see PrintReport).

## Motion

- Hover color changes over 120ms; buttons press 1px (soft) or 2px into their shadow (hard). Spinners on busy buttons and short actions; skeletons for first loads. Nothing moves under `prefers-reduced-motion`.

## Iconography

- One 76-glyph stroke set, 24px grid, 1.75px stroke, in two cuts that follow `data-edge`: round caps and circle frames (soft), square caps and square frames (hard). Use `<span class="pb-icon pb-icon--NAME">`; it paints in `currentColor`.
- Statuses: `status-ok`, `status-warn`, `status-down`, `status-info`, `status-unknown`, `status-paused`, `status-maint`. Links: `link`, `link-external`, `link-internal`. Plus objects (`server`, `database`, `network`, `file`, `folder`, `cloud`, `terminal`, `lock`, `key`) and actions (`copy`, `download`, `upload`, `refresh`, `search`, `filter`, `settings`, `trash`, `plus`, `minus`, `close`, `more`, `menu`, `sort`, `arrow-up`, `arrow-down`, `eye`, `eye-off`, `calendar`, `bell`, `inbox`) and media (`play`, `pause`, `volume`, `volume-off`, `skip-back`, `skip-forward`, `captions`, `pip`, `fullscreen`, `fullscreen-exit`, `image`, `zoom-in`, `zoom-out`) and chrome (`home`, `user`, `qr`, `sun`, `moon`, `monitor`, `grip`, `keyboard`, `shield`, `printer`, `grid`, `list`, `wrap`, `diff`, `cpu`, `chart`).
- SVG files are in the Icons Soft and Icons Hard asset groups (ink `#17150f`). No logo: set an app's name in `display` or `h1` type.
