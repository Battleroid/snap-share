# Changelog

## Unreleased
- Time-series chart: `"xFormat": "date"` in the JSON formats axis ticks (a tick every whole number of days, each under its point) and the tooltip heading as a UTC date with no time of day, for daily series.

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
