# Patchbay

## Using these files

These are the kit's docs at the commit snap vendors (`c5f2b7a` on `main` of https://github.com/Battleroid/patchbay-ui-kit): this README after this section, `CHANGELOG.md`, and `components/<Name>/README.md` plus `example.html` for every component. `make vendor-patchbay KIT=<kit clone> REF=<commit>` copies them again with the vendored files, keeps this section, and rewrites the hashes in `VENDOR.md`; `/_kit` names the commit from there. The kit's `demo.html`, `patchbay.js`, `icons/`, `app-icons/`, `tools/` and `tests/` are not copied; they live in the kit repo.

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

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="media/readme/header-dark.png">
  <img src="media/readme/header-light.png" alt="Patchbay: a dashboard and an app shell built with the kit, beside a short description of the kit and its demo">
</picture>

Patchbay is a UI kit for small self-hosted apps: status pages, dashboards, file and media sharing, config editors. It looks like a well-kept personal site. Text is set in a serif on warm paper, anything a machine would print is in mono, and a few bright fills carry the color.

It comes in two edges, **soft** (rounded, diffuse shadows) and **hard** (square, ink borders, offset shadows), and two themes, **light** and **dark**. All four share the same tokens, so switching is one attribute.

**See it:** open `demo.html`. It shows every component in every state, with switches for theme and edge, and needs nothing installed or built. Each component also has its own page, `components/<Name>/example.html`, next to a README with its markup and rules.

## Quick start

```html
<!DOCTYPE html>
<html lang="en" data-edge="soft">
<head>
  <meta charset="utf-8">
  <script src="pb-theme-boot.js"></script>
  <link rel="stylesheet" href="patchbay.css">
</head>
<body class="pb">
  <!-- markup copied from components/<Name>/example.html -->
  <script src="patchbay.js" data-pb-auto></script>
</body>
</html>
```

- `patchbay.css` has the tokens, the fonts and every component. Keep `fonts/` next to it.
- `pb-theme-boot.js` applies the saved theme before the page paints, so there's no flash. It must be a plain blocking script in `<head>`, not `defer` or a module.
- `class="pb"` marks the app root (it also resets the body margin).
- `patchbay.js` with `data-pb-auto` runs `pbInit()` once the page has loaded. Without `data-pb-auto`, nothing runs until you call it. For ES modules: `import pbInit, { pbToast, pbConfirm } from './patchbay.mjs'; pbInit();`.

Start an app from [AppShell](components/AppShell/) (skip link, header, nav, page header, footer), and a new screen from one of the page templates. Not sure which component fits? [COMPONENT-GUIDE.md](COMPONENT-GUIDE.md) answers that by job (written for AI agents, useful for anyone). Before building a part, read its README and copy the markup from its example. The examples are self-contained: each carries the script it needs, plus example-only glue such as fake saves and sample data.

## What's in it

- **Theme and text:** [ThemeSwitch](components/ThemeSwitch/), [Density](components/Density/), [TextUtils](components/TextUtils/), [Scrollbars](components/Scrollbars/), [AppIcons](components/AppIcons/), [Icons](components/Icons/)
- **Actions:** [Button](components/Button/), [ButtonGroup](components/ButtonGroup/), [Segmented](components/Segmented/), [Menu](components/Menu/), [Link](components/Link/), [CommandPalette](components/CommandPalette/), [ContextMenu](components/ContextMenu/), [BulkActions](components/BulkActions/), [Shortcuts](components/Shortcuts/), [ConfirmAction](components/ConfirmAction/), [ShareLink](components/ShareLink/)
- **Forms:** [TextInput](components/TextInput/), [InputTypes](components/InputTypes/), [Select](components/Select/), [Combobox](components/Combobox/), [TagInput](components/TagInput/), [DatePicker](components/DatePicker/), [TimePicker](components/TimePicker/), [ColorPicker](components/ColorPicker/), [CodeInput](components/CodeInput/), [CopyField](components/CopyField/), [Checkbox](components/Checkbox/), [Radio](components/Radio/), [Switch](components/Switch/), [ChoiceCard](components/ChoiceCard/), [Range](components/Range/), [DualRange](components/DualRange/), [KeyValue](components/KeyValue/), [Fieldset](components/Fieldset/), [ErrorSummary](components/ErrorSummary/), [QueryFilter](components/QueryFilter/), [MarkdownEditor](components/MarkdownEditor/), [NotifySettings](components/NotifySettings/), [AvatarEditor](components/AvatarEditor/), [CodeArea](components/CodeArea/), [DropZone](components/DropZone/), [AutoSubmit](components/AutoSubmit/)
- **Indicators:** [Badge](components/Badge/), [Pill](components/Pill/), [Notice](components/Notice/), [Toast](components/Toast/), [Notifications](components/Notifications/), [Progress](components/Progress/), [Loading](components/Loading/), [Tooltip](components/Tooltip/), [Popover](components/Popover/), [Avatar](components/Avatar/), [SiteBanner](components/SiteBanner/), [UploadQueue](components/UploadQueue/), [BusyRegion](components/BusyRegion/), [StorageRing](components/StorageRing/)
- **Data:** [Chart](components/Chart/), [ChartGround](components/ChartGround/), [Sparkline](components/Sparkline/), [StatTile](components/StatTile/), [DataTable](components/DataTable/), [DetailList](components/DetailList/), [TreeView](components/TreeView/), [Timeline](components/Timeline/), [TableTools](components/TableTools/), [Diagram](components/Diagram/), [Flow](components/Flow/), [TimeRange](components/TimeRange/), [Heatmap](components/Heatmap/), [Gauge](components/Gauge/), [BarChart](components/BarChart/), [Histogram](components/Histogram/), [JsonViewer](components/JsonViewer/), [EventCalendar](components/EventCalendar/), [VirtualList](components/VirtualList/), [QRCode](components/QRCode/)
- **Layout:** [Card](components/Card/), [ListItem](components/ListItem/), [Tabs](components/Tabs/), [Disclosure](components/Disclosure/), [Breadcrumbs](components/Breadcrumbs/), [Steps](components/Steps/), [Pagination](components/Pagination/), [EmptyState](components/EmptyState/), [Resizable](components/Resizable/), [Sortable](components/Sortable/), [CommentThread](components/CommentThread/), [MobileNav](components/MobileNav/), [SessionTimeout](components/SessionTimeout/), [Dialog](components/Dialog/), [ImageViewer](components/ImageViewer/), [MediaPlayer](components/MediaPlayer/), [Media](components/Media/), [MediaList](components/MediaList/), [Prose](components/Prose/), [SkipLink](components/SkipLink/), [MediaState](components/MediaState/)
- **Homelab:** [LogViewer](components/LogViewer/), [LogExplorer](components/LogExplorer/), [ShareDialog](components/ShareDialog/), [FilePreview](components/FilePreview/), [Terminal](components/Terminal/), [DiffViewer](components/DiffViewer/), [FileBrowser](components/FileBrowser/), [CronBuilder](components/CronBuilder/), [StorageBar](components/StorageBar/), [Rack](components/Rack/)
- **Pages:** [SiteHeader](components/SiteHeader/), [AppShell](components/AppShell/), [Dashboard](components/Dashboard/), [SettingsPage](components/SettingsPage/), [ProfilePage](components/ProfilePage/), [SignIn](components/SignIn/), [ApiTokens](components/ApiTokens/), [PrintReport](components/PrintReport/), [AlertEmail](components/AlertEmail/), [StatusPage](components/StatusPage/)

Also here: `tokens.json`, the source for every token (each is also a CSS custom property, like `var(--accent)` or `var(--space-4)`; use them instead of raw hex or px); the raw icon SVGs in `icons/soft` and `icons/hard`; app icons and a web manifest in `app-icons/`; and the alert email template, `email/alert.html`.

## Theme, edge and density

- **Edge:** `data-edge="soft"` or `"hard"` on `<html>`, one per app.
- **Theme:** `data-theme="light"` or `"dark"` on `<html>`, or on any container to theme one panel. `pb-theme-boot.js` uses the saved choice (`localStorage` `pb-theme`: `light`, `dark` or `system`), then `<html data-theme-default="light">`, then the system setting, and writes it as `data-theme` and `data-theme-choice` on `<html>`. A ThemeSwitch in the header lets people pick.
- **Density:** `data-density="compact"` on `<html>` or a container, for logs and admin lists.

## Scripting

`pbInit(root = document, {only, skip} = {})` binds every behavior under `root`, including `root` itself. Each element is bound once, so it's safe to call again after an htmx swap or after you insert markup: `pbInit(el)`. `only` and `skip` take hook names, and `data-pb-skip="name other"` on an ancestor skips those hooks inside it. If one element throws, the error is logged as `[patchbay] name` and the rest still bind. Add your own behavior with `pbHook(name, selector, fn)` before the first `pbInit()`.

`pbToast({title, body, …})` and `pbConfirm({title, body, typeToConfirm, …})` escape `body` as text. Use `bodyHTML` only for markup you trust, or pass a DOM node as `body`. `pbEsc(s)` escapes `& < > " '`.

<details>
<summary>The 91 hooks, in the order they bind (<code>pbInit.hooks</code>)</summary>

`vars`, `toasts`, `kbd`, `tip`, `scroll`, `dot`, `progress`, `datefield`, `timefield`, `combo`, `tags`, `codeinput`, `swatches`, `colorfield`, `copy`, `reveal`, `kv`, `range2`, `toggle`, `indeterminate`, `count`, `stepper`, `colortext`, `strength`, `range`, `tabs`, `section`, `open`, `modal`, `inbox`, `table`, `player`, `codearea`, `shell`, `sitehead`, `trange`, `refresh`, `ts`, `share`, `fpreview`, `dt`, `evcal`, `md`, `thread`, `notify`, `dropset`, `sheet`, `session`, `themeswitch`, `popover`, `hovercard`, `menu`, `tree`, `cmd`, `contextmenu`, `imageviewer`, `avatar`, `avataredit`, `pager`, `validate`, `reltime`, `midtrunc`, `clamp`, `qr`, `resizable`, `log`, `logx`, `diff`, `files`, `term`, `cron`, `storage`, `rack`, `heat`, `gauge`, `gaugedonut`, `bars`, `hist`, `json`, `bulk`, `qf`, `sortable`, `keys`, `upq`, `sectionnav`, `dismiss`, `confirm`, `autosubmit`, `teaser`, `sharelink`, `ring`

</details>

<details>
<summary>Everything <code>patchbay.mjs</code> exports</summary>

`pbInit` (also the default export), `pbConfirm`, `pbToast`, `pbInbox`, `pbTheme`, `pbPlace`, `pbCalendar`, `pbClock`, `pbQRSvg`, `pbQRCode`, `pbVirtual`, `pbAnsi`, `pbCronParse`, `pbSection`, `pbHook`, `pbEsc`, `pbStyled`, `pbStrings`, `pbId`, `pbChartHover`, `pbCronHuman`, `pbCronNext`, `pbMarkdown`, `pbScrollRegion`, `pbRelTime`, `pbPopovers`, `pbCodeHL`, `pbCodeArea`, `pbQR`, `pbPlayer`, `pbVars`, `pbListKeys`, `pbAnnounce`, `pbKey`, `pbMark`, `pbEmit`, `pbFocusNear`, `pbStrength`, `pbTip`, `pbGridKeys`, `pbScroller`, `pbFieldError`, `pbDiagramText`, `pbT`, `pbNum`, `pbLocale`, `pbParts`, `pbNotice`, `pbNoticeInto`, `pbBusy`, `pbShortcuts`, `pbGuardUnload`, `pbProcessing`, `pbShareLink`, `pbFmt`

</details>

## Strict CSP

The kit needs no inline script or style. It runs under:

```
default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; font-src 'self'
```

- Add `media-src 'self' blob:` for the media player and its captions. Add `connect-src 'self'` (or your origin) for the parts that fetch: the lazy notifications inbox (`data-src`) and the command palette's `data-cmd-src`.
- Keep `img-src data:` (icons are CSS masks on `data:` URIs) and `blob:` (photos in the avatar editor).
- The kit never uses `eval`, `new Function`, inline handlers or `style` attributes. Markup it builds as a string carries its styles in `data-style`, and `pbStyled(container)` moves them into the CSSOM. Call it after inserting your own strings too.
- In your own markup, set custom properties with `data-vars="--w:42%;--c:var(--chart-2)"` (names `--a-z0-9`; values without `;`, `url(` or `expression`). Layouts that need a number take it as an attribute: `.pb-dash > [data-span="1..12"]`, `.pb-steps[data-n]`, `.pb-tabbar[data-n]`, `.pb-qr[data-size=sm|md|lg]`, `.pb-auth--inline`, `.pb-progress[data-value]`.
- `tests/fixtures/csp.html` runs every component under this policy, built from the same markup as the examples. The `pb-demo-*` classes in `demo.css` only lay out the examples, so don't link `demo.css` in your app.

## Working on the kit

Everything in the repo root is generated from `tools/src`. `tools/build.sh .` rebuilds it, and `npm test` runs the accessibility, CSP, layout and screenshot checks. `tools/README.md` covers where things live, how to add a component, and how the header image above is drawn (`npm run readme:header`).

## License

MIT for the code and docs (`LICENSE`). The fonts are under the SIL Open Font License 1.1 (`fonts/OFL.txt`).

## Design guide

How the kit should look, read and fit together (content, color, type, shape, controls, layout, motion and icons) is in [DESIGN.md](DESIGN.md). Follow it when you build on the kit, too.
