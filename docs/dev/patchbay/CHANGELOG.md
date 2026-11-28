# Changelog

## Unreleased
- **AvatarEditor** (new): a photo field (`.pb-avfield[data-avatar-edit]`, hook `avataredit`) that takes a picked, dropped or pasted PNG, JPEG or WebP, refuses the wrong type, size or dimensions in its hint, and crops it square in a dialog (drag, wheel, pinch, zoom slider, arrows, `+`/`-`, `R` to rotate, `0` to reset) with live 64/44/32/24 px previews. The frame is round in soft and square in hard. Save fires `pb:avatar` with a re-encoded blob (camera metadata gone) and the kept `crop`; remove and Undo fire it too. The app settles with `pb:saved` or `pb:error`. `data-avatar-sync` updates other avatars of the same person, keeping presence dots.
- **ProfilePage** (new): a person's own account on the SettingsPage layout, with photo, about you, sign-in (password dialog, two-factor, passkeys), sessions with sign-out, preferences that apply at once, and account deletion behind type-to-confirm.
- Settings layout: two panels in one section (a photo above a form) keep the `space-6` card gap instead of touching.
- README: rewritten as an overview (what it is, quick start, components grouped as in the demo, theming, scripting, CSP), with the hook and export lists folded away. The design guide moved to `DESIGN.md`, in plainer words; no rule changed.
- `COMPONENT-GUIDE.md` (new): which component to use for each job, with the one it's most often confused with, for AI agents designing with the kit. The build fails if a component is missing from its index. `CLAUDE.md` (new): how the repo is built and tested, and how to add or remove a component.
- Time-series chart: `"xFormat": "date"` in the JSON formats axis ticks (a tick every whole number of days, each under its point) and the tooltip heading as a UTC date with no time of day, for daily series.

### Breaking (see the migration table below)
- **Fields:** `div.pb-field` > `label.pb-field__label[for]` + the control with `aria-describedby` (help, error) replaces `label.pb-field` wrapping everything, which made help and error text part of the control's name. Errors are `.pb-field__error#…-error` plus `aria-invalid` on the control; `[data-invalid]` / `:has([aria-invalid=true])` style it. Several controls under one label: `div.pb-field[role=group][aria-labelledby]`.
- **Events:** every component event is `pb:<name>` (bubbling `CustomEvent`). Value controls keep firing native `input`/`change`; a non-value component `change` is `pb:change`.
- **Openers:** one `data-pb-open="#id"` for dialogs, sheets, the shortcut sheet and the palette (fires `pb:show`); `data-open`, `data-cmd-open`, `data-keys-open`, `data-sheet-open` and `pbDialogOpen` are gone.
- **Names:** hooks `otp`, `split`, `lightbox`, `dial`, `donut` are `codeinput`, `resizable`, `imageviewer`, `gauge`, `gaugedonut` (and their `data-*` attributes); `.pb-gauge` → `.pb-progressrow`, `.pb-banner` → `.pb-statusbanner`, `.pb-spin` → `.pb-icon--spin`.
- **Library fakes removed:** NotifySettings "Test" fires `pb:test` and DropSet "Save as default" fires `pb:default`; both wait for the app's `pb:saved` / `pb:error` instead of reporting success on their own.
- **Tokens and look:** `--chart-2` is violet, not red (red means state only); `--info` is teal and no longer aliases the accent; dark `--ink-muted` and `--pop-mint` are lighter; Pill `aria-pressed` is tint + border; the bell count is ink; disabled buttons keep a `--line-strong` edge; off-scale type sizes moved to the scale (10→11, 12.5→13, 21→19, 22→24, 30→28, 96→56); breakpoints are 480, 640 and 900; no `data-theme` now follows the system's dark setting in CSS alone.
- **Behaviour:** `pbValidate` no longer stops other submit listeners (check `e.pbInvalid`; cancelable `pb:invalid`) and focuses the first invalid field; toasts announce through one live region, queue three at a time, keep errors until dismissed and keep action toasts at least 10 s (F8 jumps to them); editable CodeArea is plain text unless `data-lang`; VirtualList is a `listbox`; TableTools is one `role=grid` tab stop; LogExplorer, context menus, bars and the calendar rove; stepper buttons are not tab stops; QueryFilter's Tab and Space no longer commit a preselected option.

### Added
- App patterns that apps kept rebuilding: `pbNotice` / `pbNoticeInto`; the confirm hook (`data-confirm`, `-body`, `-ok`, `-variant`, `-type`, and `data-confirm-enable` for controls served disabled until bound); `form[data-autosubmit]`; busy regions (`[aria-busy]` with a delayed spinner, `pbBusy`, an htmx recipe); `pbShortcuts` (a registry that also fills the `?` sheet); a data-driven command palette (`data-cmd-src` or a JSON block); ShareLink (Copy link + QR); StorageRing; MediaState (`.pb-proc`) and the hover/focus teaser; compact Result and media tile; bulk select-all-matching, Shift ranges and tile select-all; UploadQueue retry, cancel, remove and cancel all; `pbGuardUnload`; Tabs `data-tab-hash` and `data-hides`; pluggable strength scoring (`pbStrength.score`); CodeInput `data-name` (a hidden named input); TagInput `data-pattern`; date and time field error lines (`pbFieldError`).
- Accessibility: a real tooltip (role, `aria-describedby`, Esc, hoverable, flips); a modal phone drawer (focus moves in, the rest is `inert`, scrim); one list-key helper (`pbListKeys`, `pbGridKeys`) for menus, context menu, mega menu, combobox, palette and grids; chart read-outs on arrow keys; named scroll regions with edge fades (`scroll` hook, `pbScroller`); dialogs get `aria-labelledby` / `aria-describedby` and lock page scroll; Range and DualRange values, Progress names, TimePicker listboxes; reltime and middle-truncated values reachable and copyable; platform key hints (`pbKey`); `pbStrings` for every word the kit writes; `pbId`.
- Tokens: `--pb-text-*`, `--pb-dur-1..3` and `--pb-ease`, `--pb-z-*` layers, `--pb-focus-*`, paper/scrim colours; logical properties throughout (a `dir=rtl` smoke test).
- Icons: edit, share, mail, log-out, tag, chevron-left, chevron-up. Hard-edge status glyphs stay round.
- Components: Confirm action, Auto-submit, Busy region, Media state, Share link, Storage ring (116 in all).

### Fixed
- Passes axe WCAG 2.2 AA (target size included) in all four theme × edge combos at 1280 and at 375 px. Focus indicators on menus, options and accent-filled items are ≥ 3:1; targets are ≥ 24 px (small buttons 28 px); reduced motion stops every spinner, pulse and stripe.
- TableTools column resize works (mouse and ←/→); the EventCalendar grid moves with the keys and gives phone users 48 px day cells; the first toast is announced; context menus no longer double-bind on a second `pbInit`; FilePreview no longer scrolls the page at load; the site header's Esc no longer steals focus; reduced-motion rules the build dropped are back.
- Phone layouts: stacked tables (`.pb-tablescroll[data-stack]`), split panes and KeyValue stack, ListItem meta wraps, the shell search collapses to an icon, NotifySettings keeps its first column, and the image viewer keeps its arrows (44 px touch targets).

### Docs, examples and tooling
- Every component README has Markup, States, Keys, Events and Do and don't. Examples carry no inline styles, handlers or scripts (demo-only styling is in `demo.css`), so the example markup is what runs under the fixture's CSP; the fixture covers all 116 components.
- The README opens with a header image drawn with the kit (`npm run readme:header`) and says how to open the demo; the dead GitHub Pages link is gone.
- `tools/build.sh` runs on Linux, WSL and Git Bash with any Python 3 (zip optional) and replaces generated folders; tests serve the build with a dependency-free Node server (`PB_PORT`, `PB_ROOT`) and run axe in all four combos; `.gitattributes` keeps text LF.

| Old | New |
|---|---|
| `<label class="pb-field"><span class="pb-field__label">` … help … `</label>` | `<div class="pb-field"><label class="pb-field__label" for="ID">` … control `id="ID" aria-describedby="ID-help ID-error"` … `<span class="pb-field__help" id="ID-help">` |
| error as `.pb-field__help` with an icon | `.pb-field__error#ID-error` + `aria-invalid="true"` |
| `data-open="id"`, `data-cmd-open`, `data-keys-open`, `data-sheet-open` | `data-pb-open="#id"` (+ `pb:show` on the dialog) |
| events `select`, `page`, `expand`, `command`, `complete`, `open`, `close`, `cancel`, `retry`, `remove`, `reorder`, `bulk`, `post`, `stay`, `expire`, `choose`, `test`, `default`, `refresh`, `zoom`, `invite`, `stop`, `edit`, `export`, `drop-files` | `pb:` + the same name |
| component `change` (QueryFilter, NotifySettings, DropSet, time range, share, cron, DataTable columns) | `pb:change` |
| hooks/attributes `otp`/`data-otp`, `split`/`data-split`, `lightbox`/`data-lightbox(-item)`, `dial`/`data-dial`, `donut`/`data-donut` | `codeinput`, `resizable`, `imageviewer(-item)`, `gauge`, `gaugedonut` |
| `.pb-gauge`, `.pb-banner`, `.pb-spin` | `.pb-progressrow`, `.pb-statusbanner`, `.pb-icon--spin` |
| `pbShortcuts(dialog)` (hook callback) | `pbShortcuts(map, opts)` registry; the hook runs `pbKeysSheet` |
| Notify "Test" / DropSet "Saved" succeed by themselves | the app answers `pb:test` / `pb:default` with `pb:saved` or `pb:error` |
| `pbUid` | `pbId` |

## 2026-11-24
- Compact upload settings (`pb-dropset--compact`): labelled fields in three, two or one columns, with a one-line `pb-drop--slim` drop zone.
- Media list for videos: thumbnail on the left with duration, watched bar and state; title, details, tags, links and actions on the right; a compact variant.
- Drop zone upload settings: a summary bar above the drop zone opens a settings panel (`pbDropSet`, hook `dropset`).
- `pbChartHover(svg, spec)` adds a crosshair or highlighted bar and a tooltip to hand-drawn SVG charts; Chart and the chart grounds use it, with arrow keys.
- Dialog buttons close the dialog again in the demo page (it no longer blocks `method="dialog"` submits); long dialog text spans the full width.
- Stacked site header names and taglines left-align when the header is narrow. Table tool detail rows use a tighter label-over-value grid. File preview details no longer clip.
- Strict CSP: the kit runs under `default-src 'none'; script-src 'self'; style-src 'self'` (plus `media-src` for media). Script-built markup carries `data-style`, applied by `pbStyled()`; markup sets custom properties with `data-vars` and attribute forms (`data-span`, `data-n`, `data-size`, `pb-auth--inline`, `.pb-progress[data-value]`). No `eval`, `new Function`, inline handlers or style attributes.
- `patchbay.mjs` (ES module with named exports and `pbInit` as the default) beside `patchbay.js`; `pb-theme-boot.js` replaces the inline no-flash theme snippet.
- `pbInit(root, {only, skip})` is idempotent: hooks are registered with `pbHook(name, selector, fn)`, each element binds once, `data-pb-skip` opts out, one failing element no longer stops the rest. `patchbay.js` runs it on DOMContentLoaded only with `data-pb-auto`.
- Form sections track real dirty state against a snapshot, show busy while saving, and settle on `pb:saved` / `pb:error`; Discard restores the snapshot and no longer disables reveal buttons.
- `pbToast` and `pbConfirm` escape `body`; `bodyHTML` is the opt-in for markup, and a Node can be passed. Confirm dialogs are labelled and described. `pbInbox(el, {unread, lazy, onOpen, onRead, onReadAll})`.
- Tabs fire `pb:tabchange` and leave link tabs alone; steppers fire `input`; copy buttons take `data-copy-value` and `data-copy-target`; tables skip `[data-dt]` and `[data-bulk]`; menus take `data-align="end"` on the trigger; new `progress`, `vars`, `codearea` and `indeterminate` hooks.
- Popovers, date and time pickers, colour fields, the inbox, context menus, the shell and the site header share one capturing document listener per event type (`pbOnDoc`) and one placement function (`pbPlace`).
- One `pbEsc` escapes `& < > " '` everywhere. Component scripts are generated from the shared sources (`tools/src/gen/scripts.json`), so the copies in examples cannot drift.
- Layout fixes: `body.pb` has no margin; the AppShell drawer stretches from under the header to the viewport bottom and scrolls inside; `--pb-head-h` on `.pb-shell` offsets the sticky settings nav, `pb-layout__aside`, anchor jumps and the section-nav highlight; `pb-layout` stacks at 900px like the drawer; `pb-form__row` top-aligns fields; choice-card and choice legends get the 6px label gap; a lone link in a mixed button group keeps square sides; the site header keeps its theme switch on phones (copied into the phone menu) and hides the brand host under 480px.
- Additions: hued tag pills (`.pb-pill[data-hue="1".."6"]`) and pills centred in cells and tiles; `pb-qr--bare` for popovers; `pb-pagehead__meta`; `pb-item__lead--thumb` / `--wide` and the `pb-mediagrid` media-tile grid; `pb-sortable--tiles`; `data-upq-controls` on upload queues; lightbox members via `a[data-lightbox-item]` (or `data-lightbox="selector"`) with video and audio played in the stage; read-only code blocks stay selectable.
- Changed defaults: `.pb-midtrunc` inherits its font (add `pb-midtrunc--mono`); `pb-layout__aside` sticks at `--pb-head-h` + 16px (16px outside a shell, was 72px).
- Behavior: TagInput keeps its tags in a hidden form field (`data-name`, `data-sep`) with `pbSet`/`pbValues`; the `bulk` hook only binds `[data-bulk]` containers that hold a bulk bar; `pbInbox` accepts `lazy: true` with `data-inbox-src`, fires `pb:read` and `onRead(item, wasUnread)` for every item, and read-all fires a cancelable `pb:readall`; clean form sections start with Save and Discard disabled; popover menus get ↑/↓, Home/End and typeahead; tabs get a roving tabindex with arrow, Home and End keys.
- Lightbox reads its members when it opens (`gallery.pbOpen(i)` from script), returns focus to the opener only if nothing else took it, and lets modifier and middle clicks open links normally. Player shows `--:--` or `data-duration` until the length is known and caps video at `--pb-player-max-h` (80vh). Prose tables are styled. Sortable tiles block touch scrolling only on the handle and get 2D arrow keys on their own. Table number, mono and action cells no longer wrap; segmented controls and button groups keep their own width in grids; inline code in labels keeps its case; progress bars ease (not under reduced motion).
- Accessibility: the demo now passes axe (WCAG 2.1 AA, serious and critical) in both themes. The log explorer's timestamp is the expand button; the file browser is a multi-select listbox and names its sort state on the header buttons; event calendar weeks are grid rows; toasts live in a labelled region (not a list); terminal output and file details are focusable, named scroll regions (`pbScrollRegion`); the open list row uses `aria-current`; loading cards are labelled groups; markdown task boxes have labels; resolved comments, error log levels and edge diagram nodes meet contrast.
- Breaking: raw HTML passed as `body` to `pbToast` or `pbConfirm` is now shown as text; pass it as `bodyHTML`. `pbInbox`, `pbPopovers` and `pbRelTime` bind through `pbInit`, so calling them twice no longer doubles listeners.

## 2026-11-23
- Time range and auto-refresh pickers; zoomable time-series chart with crosshair and legend toggles.
- Share dialog, file preview pane, table tools (columns, resize, density, expandable rows, inline edit, CSV, sticky column).
- Event calendar, markdown editor, comment thread, notification settings grid, phone tab bar and action sheet, session timeout warning.
- Site header variants (links, dropdown menus, utility bar, sub-nav, sticky, phone menu).
- Compact density, Windows high-contrast support, app icons and web manifest, alert email template.
- Sign-in logo variants; two-factor setup shows recovery codes only after the code is verified.
- Log explorer (Loki-style labels and LogQL), joined storage bar, refined API token scopes, roomier chart tooltips.
- Build tools in `tools/`, MIT license, font licenses, CI checks.

## 2026-11-22
- Homelab views: log viewer, terminal output, diff viewer, file browser, cron builder, storage bar, rack.
- Charts: bar, histogram, gauges and donuts, calendar heatmap, JSON/YAML viewer.
- Bulk actions, query filter, sortable lists, shortcut sheet, upload queue, virtual list.
- Page templates: dashboard, settings, sign-in, API tokens, print report. Themed scrollbars.
- Dark theme; app shell and site-wide patterns (breadcrumbs, loading, empty states, tree view, command palette, popovers, and more).
- First release: tokens, soft and hard edge variants, forms, overlays, media player, charts, status page.
