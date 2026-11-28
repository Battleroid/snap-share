# Patchbay design guide

How the kit should look, read and fit together. Setup and the component list are in the [README](README.md).

## Content

Write plainly and briefly, in sentence case for titles and buttons ("Copy link", "Recent incidents"). UI chrome is lowercase: meta labels, pill text, badge words (`up`, `degraded`, `expires`). Real names keep their casing (`Jellyfin`, `Home Assistant`).

Hosts, ports, paths, IDs, sizes and times are set in mono and shown in full: `nas-01.lan:5001`, `2026-11-21`, `4.1 GB`. Join a label and its scope with `·` (`uptime · 30d`), and use `—` for a missing value. Units follow the number, smaller, in `ink-muted`.

Helper and incident text says what happened and what to do next: "Port must be 1–65535.", "Raised maxmemory; watching response times."

No emoji. A state is always shown with an icon, a word and a color, never color alone.

## Color

The page ground is `bg`, a warm paper. Cards, inputs and buttons sit on `surface` (white). `surface-sunk` fills hover rows, kbd, inline code and meter tracks, and code sits on `code-bg`.

Text is `ink` for content and values and `ink-muted` for hosts, captions, help and axis labels. Both pass 6:1 on every ground in both themes.

`accent` is the only interactive color: links, the primary button, the selected pill, the focus ring and the first chart series. `accent-tint` marks selection and drag-over.

The pop fills, `pop-yellow`, `pop-mint` and `pop-red`, are where the color lives: card bands, the status banner, solid badges, pop pills, diagram roles, image placeholders. Text on them is `on-pop` (ink). Use two pops at most in one view.

Status colors mean state and nothing else: `ok`, `warn`, `down`, `info` and `unknown`, each with a `*-tint` ground for badges. Don't use them for decoration. `ok` and `down` differ only by hue, which is why the icon and word are required.

- Code: `syn-keyword`, `syn-string`, `syn-number`, `syn-function`, `syn-type` and `syn-comment` (italic), all readable on `code-bg` and `surface`.
- Terminal output: the `term-*` colors (red, green, yellow, blue, magenta, cyan), the same in both themes and 7:1 or better on `night`.
- `night` and `on-night` don't change with the theme. They're for video stages, the image viewer, the avatar crop, ink tiles and ink toasts. QR codes are always dark on white.
- Charts: `chart-1` (accent), `chart-2` (vermilion), `chart-3` (dashed baseline) and `chart-4` (ochre), with `chart-grid` for rules. Four series at most.

Every color token has a dark value with the same role and the same contrast rules (4.5:1 for text, 3:1 for marks). Accent and status colors get lighter, tints get darker, and `on-accent` turns dark. Never hard-code a hex: a raw color always breaks one of the themes.

Windows high contrast (`forced-colors: active`) is handled in the stylesheet, so borders, focus rings, selection and chart series stay visible. That only works if meaning never rides on a background color alone.

## Type

- `serif` (Source Serif 4, falling back to Times New Roman) for titles, prose, card titles, service and node names, and help text.
- `mono` (Obel Mono) for controls (`ui`), code (`code`), labels (`label`, uppercase and tracked) and every number (`metric`, `metric-xl`), with tabular figures.

Prose is `body` (17/27) at `size-measure` (68ch). Dense UI copy is `body-sm`, and captions and help are `note` (italic). Use one `display` or `metric-xl` per page.

## Shape and depth

**Soft** uses `radius-lg` for cards, panels and drop zones, `radius-md` for buttons, inputs, code and nodes, `radius-sm` for badges and kbd, and `radius-pill` for pills. Cards have `line` borders and controls `line-strong`. Shadows are `shadow-soft-sm`, `-md` and `-lg`.

**Hard** is `radius-0` everywhere, with `ink` borders and offset shadows: `shadow-hard-sm` (2px) on controls, `shadow-hard-md` (4px) on cards, `shadow-hard-lg` (6px) on popovers and active drop zones. Buttons press into their shadow on `:active`.

Both use `shadow-inset` on fields, `stroke-1` borders, and a `stroke-2` focus ring in `accent` with a 2px offset on everything focusable. Rules and dividers are `line` hairlines, which turn `ink` in hard. Use one edge per app; don't mix them on a page.

## Controls

Build on native elements styled with `pb-` classes: `pb-check`, `pb-radio`, `pb-switch`, `.pb-select`, `.pb-input` (every input type), `pb-range`, `pb-progressbar`, `pb-meterbar`, `pb-fieldset`, `pb-details`, `pb-dialog`, `[popover].pb-menu` and `table.pb-table`. Keyboard, form and accessibility behavior then comes from the browser.

Every field has a label (`pb-field__label`, uppercase mono). Help and errors go under the field in `note` style, and errors are in `down` with the `status-down` icon. Forms with more than two fields get an ErrorSummary at the top. A form section keeps its Save and Discard buttons inside its panel (see Fieldset).

Pick a control by what it's for:

| For | Use |
| --- | --- |
| Independent options in a form | Checkbox |
| A setting that applies at once | Switch |
| 2–6 exclusive options with hints | Radio |
| A short exclusive choice in a toolbar | Segmented |
| More than six options | Select |
| One value from a long list | Combobox |
| Many values | TagInput |
| Options that need a description | ChoiceCard |
| Dates and times | DatePicker, TimePicker (the browser's popups can't be styled) |
| A color | ColorPicker |
| A one-time code | CodeInput |
| A value to copy | CopyField |
| Env vars and headers | KeyValue |
| A min–max range | DualRange |
| A profile photo | AvatarEditor |

Conjoined controls: `pb-seg` for toggles (radios or checkboxes), `pb-btngroup` for related actions, split buttons and pagers, and `pb-toolbar` for a row of mixed controls.

**Finding things.** AppShell's nav, Breadcrumbs, CommandPalette (⌘K), Pagination, and TreeView for nested storage. Context menus repeat actions that exist elsewhere; never put an action only there.

**Lists and records.** ListItem rows, DetailList for one thing's fields, Data table to compare many, and Timeline for a record of events. Lists people filter often get a QueryFilter (`status:down host:nas-*`), and selected rows get a BulkActions bar. Big tables use TableTools (columns, density, expandable rows, inline edit, CSV). Past about 2,000 rows, use VirtualList. Empty and failed states use EmptyState and Result; loading uses skeletons or a spinner (Loading).

**Homelab views.** LogViewer for live logs, Terminal for command output, DiffViewer for config changes, FileBrowser for storage, CronBuilder for schedules, StorageBar for pools, and Rack for hardware. Scheduled work shows in an EventCalendar.

**Time.** Views over time put a TimeRange picker and auto-refresh in the page header. The time-series chart zooms by drag and follows the picker.

**Charts.** Chart for lines over time, BarChart for amounts per category (stacked or horizontal), Histogram for distributions, Gauge for one value against limits, donuts for parts of a whole (five at most), and Heatmap for per-day activity or pass/fail. JsonViewer shows structured data.

**Feedback.** Toast for the result of someone's own action (with Undo where possible), Notice for a problem on the page, and Notifications for what happened while they were away. Use a Dialog only to confirm something or to ask for input that can't wait. SessionTimeout warns a minute before sign-out, and alert emails use the AlertEmail template.

**Media and the rest.** Video and audio always use MediaPlayer, never the browser's own controls. Reorderable things use Sortable, which always has a keyboard path. Uploads report progress in an UploadQueue. Every app lists its shortcuts in a Shortcuts sheet on `?`. Panels people pick or open are `pb-card--outline`, a quiet outline that turns `accent` on hover and focus.

## Layout

Everything sits on a 4px grid, `space-1` to `space-12`. Cards have `space-6` padding and `space-6` between them, and sections have `space-8` between them. Pages are at most `size-page` (1120px) wide, centered, with `space-8` gutters, and prose columns at most `size-measure`. Controls are `size-control` (36px) tall, or `size-control-sm` (28px).

Apps start from AppShell, with a 240px nav that becomes a drawer under 900px. Page content is a `pb-pagehead` followed by `pb-layout` (content plus a 300px aside, which stacks when narrow). Resizable splits list and preview panes. Site banners sit above the shell header, one at a time. Dense views (log tables, admin lists) set `data-density="compact"` for 28px controls and tighter rows and cards.

Public sites and docs use SiteHeader instead (brand, links or dropdown menus, search, theme, one call to action); under 760px the links fold into a menu. Phone-first apps use MobileNav: a bottom tab bar (five tabs at most) and action sheets.

Start new screens from a page template:

- Dashboard: a 12-column `pb-dash` grid.
- SettingsPage: section nav, setting rows, danger zone last.
- ProfilePage: someone's own account, on the SettingsPage layout.
- SignIn: sign-in, two-factor setup and the code prompt.
- ApiTokens: show a token once, revoke it later.

Every app ships the AppIcons head snippet (favicon, touch icon, manifest, `theme-color`). Scrollbars inside `.pb` are thin and themed. Printing hides app chrome, drops shadows and expands scroll areas, and reports use `pb-report` (see PrintReport).

## Motion

Hover colors change over 120ms. Buttons press 1px (soft) or 2px (hard) into their shadow. Busy buttons and short actions get a spinner, and first loads get skeletons. Nothing moves under `prefers-reduced-motion`.

## Icons

One set of 76 stroke icons on a 24px grid with a 1.75px stroke, in two cuts that follow `data-edge`: round caps and circle frames for soft, square caps and square frames for hard. Write `<span class="pb-icon pb-icon--NAME"></span>`; it paints in `currentColor`.

- Status: `status-ok`, `status-warn`, `status-down`, `status-info`, `status-unknown`, `status-paused`, `status-maint`.
- Links: `link`, `link-external`, `link-internal`.
- Objects: `server`, `database`, `network`, `file`, `folder`, `cloud`, `terminal`, `lock`, `key`.
- Actions: `copy`, `download`, `upload`, `refresh`, `search`, `filter`, `settings`, `trash`, `plus`, `minus`, `close`, `more`, `menu`, `sort`, `arrow-up`, `arrow-down`, `eye`, `eye-off`, `calendar`, `bell`, `inbox`.
- Media: `play`, `pause`, `volume`, `volume-off`, `skip-back`, `skip-forward`, `captions`, `pip`, `fullscreen`, `fullscreen-exit`, `image`, `zoom-in`, `zoom-out`.
- Chrome: `home`, `user`, `qr`, `sun`, `moon`, `monitor`, `grip`, `keyboard`, `shield`, `printer`, `grid`, `list`, `wrap`, `diff`, `cpu`, `chart`.

The SVGs are in the Icons Soft and Icons Hard asset groups (ink `#17150f`). There's no logo: set an app's name in `display` or `h1` type.
