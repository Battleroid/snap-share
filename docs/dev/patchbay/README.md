# Patchbay

## Using these files

- Link `patchbay.css` (tokens, fonts, every component). Fonts load from `fonts/` beside it; keep the two together.
- Put `class="pb"` on the app root and `data-edge="soft"` or `data-edge="hard"` on `<html>`.
- Before building a part, read `components/<Name>/README.md` for its markup and rules, and copy the markup from `components/<Name>/example.html`.
- Tokens: `tokens.json` is the source; every value is also a CSS custom property (`var(--accent)`, `var(--space-4)`, `var(--radius-md)`). Use tokens, never raw hex or px.
- Icons: use `<span class="pb-icon pb-icon--NAME"></span>` (paints in `currentColor`). Raw SVGs are in `icons/soft` and `icons/hard`.
- `demo.html` shows every component on one page.
- Components: Badge, Button, ButtonGroup, Card, Chart, ChartGround, Checkbox, CodeArea, DataTable, Diagram, Dialog, Disclosure, DropZone, Fieldset, Flow, Icons, InputTypes, Link, Media, Menu, Notice, Pill, Progress, Prose, Radio, Range, Segmented, Select, Sparkline, StatTile, StatusPage, Switch, Tabs, TextInput, Tooltip.

A light, document-first system for small self-hosted apps: status pages, dashboards, file and media sharing, config editors. Text is set like a well-kept personal site — a comfortable serif on warm paper, mono for anything a machine would print — with a few saturated pop fills for color. It ships in two edge variants that share every color and type token: **soft** (rounded, diffuse shadows) and **hard** (square, ink borders, offset shadows).

## Setup

- Load `tokens.css`, then `components/bundle.css`. Put `class="pb"` on the app root.
- Pick a variant with `data-edge="soft"` (default) or `data-edge="hard"` on `<html>` or any container. Use one variant per app; do not mix them on a page.
- Components are CSS only. Class prefix is `pb-`. Previews show both variants side by side.

## Content

- Write plainly and briefly. Sentence case for titles and buttons ("Copy link", "Recent incidents").
- Lowercase for UI chrome: meta labels, pill text, badge words (`up`, `degraded`, `expires`). Real names keep their casing (`Jellyfin`, `Home Assistant`).
- Hosts, ports, paths, IDs, sizes and times are mono and shown in full: `nas-01.lan:5001`, `2026-11-21`, `4.1 GB`.
- Join a label and its scope with `·` (`uptime · 30d`). Use `—` for a missing value. Units follow the number, smaller, in `ink-muted`.
- Incident and helper text says what happened and what to do: "Port must be 1–65535.", "Raised maxmemory; watching response times."
- No emoji. State is always icon + word + color.

## Color

- Ground is `bg` (warm paper). Cards, inputs and buttons sit on `surface` (white). `surface-sunk` fills hover rows, kbd, inline code, meter tracks. Code sits on `code-bg`.
- Text: `ink` for content and values, `ink-muted` for hosts, captions, helper text and axis labels. Both pass 6:1 on every ground.
- `accent` is the one interactive color: links, the primary button, the selected pill, the focus ring, chart-1. `accent-tint` marks selection and drag-over.
- Pop fills — `pop-yellow`, `pop-mint`, `pop-red` — carry color: card bands, the status banner, solid badges, pop pills, diagram roles, image placeholders. Text on them is `on-pop` (ink). Use at most two pops in one view.
- Status colors mean state only: `ok`, `warn`, `down`, `info`, `unknown`, each with a `*-tint` ground for badges. Never use them for decoration. `ok` and `down` differ by hue, so the icon and word are required.
- Code: `syn-keyword`, `syn-string`, `syn-number`, `syn-function`, `syn-type`, `syn-comment` (italic); all read on `code-bg` and `surface`.
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
- Pick the control by intent: Checkbox for independent options in a form, Switch for settings that apply at once, Radio for 2–6 exclusive options with hints, Segmented for short exclusive choices in a toolbar, Select for more than six.
- Conjoined controls: `pb-seg` for toggles (radios or checkboxes), `pb-btngroup` for related actions, split buttons and pagers, `pb-toolbar` for a row of mixed controls.
- Labels on every field (`pb-field__label`, uppercase mono). Help and errors go under the field in `note` style; errors in `down` with `status-down`.
- Panels people pick or open are `pb-card--outline`: a quiet outline that turns `accent` on hover and focus.

## Layout

- 4px grid: `space-1` … `space-12`. Card padding `space-6`, gaps between cards `space-6`, between sections `space-8`.
- Pages max `size-page` (1120px), centered, with `space-8` gutters. Prose columns max `size-measure`.
- Control height `size-control` (36px), small `size-control-sm` (28px).

## Motion

- Hover color changes over 120ms; buttons press 1px (soft) or 2px into their shadow (hard). Spinners only on busy buttons. Nothing moves under `prefers-reduced-motion`.

## Iconography

- One 45-glyph stroke set, 24px grid, 1.75px stroke, in two cuts that follow `data-edge`: round caps and circle frames (soft), square caps and square frames (hard). Use `<span class="pb-icon pb-icon--NAME">`; it paints in `currentColor`.
- Statuses: `status-ok`, `status-warn`, `status-down`, `status-info`, `status-unknown`, `status-paused`, `status-maint`. Links: `link`, `link-external`, `link-internal`. Plus objects (`server`, `database`, `network`, `file`, `folder`, `cloud`, `terminal`, `lock`, `key`) and actions (`copy`, `download`, `upload`, `refresh`, `search`, `filter`, `settings`, `trash`, `plus`, `minus`, `close`, `more`, `menu`, `sort`, `arrow-up`, `arrow-down`, `eye`, `eye-off`, `calendar`).
- SVG files are in the Icons Soft and Icons Hard asset groups (ink `#17150f`). No logo: set an app's name in `display` or `h1` type.
