The time range picker, the auto-refresh picker and the time-series chart work together for logs and metrics.

## Markup

- Time range: `.pb-trange[data-trange][data-from][data-to]` with a `pb-btngroup` (earlier `[data-trange-back]`, the `[data-trange-open]` button with `pb-trange__label`, later `[data-trange-fwd]`, zoom out `[data-trange-zoom]`) and a `pb-popover.pb-trange__pop` holding the absolute From/To fields (`[data-tr-from]`, `[data-tr-to]`, `[data-tr-err]`, `[data-tr-apply]`), recently used ranges and a searchable quick list. `el.pbSet(from, to)`, `el.pbRange()`.
- Auto-refresh: `.pb-btngroup.pb-refresh[data-refresh]` with a Refresh button, a `[data-refresh-menu]` button (`pb-refresh__dot`, `[data-refresh-label]`) and a `pb-menu` of `[data-interval]` items in seconds (0 = off).
- Time-series chart: `figure.pb-ts[data-ts]` with `pb-ts__head` (eyebrow, `pb-ts__reset`), an empty `pb-ts__plot` (`--h` height), `ul.pb-ts__legend` and a `<script type="application/json">` of `{series: [{name, c, data: [[ms, value]], dash}], unit, max, threshold: {v, label}, area, xFormat}`. `el.pbSetData(d)`, `el.pbZoom(a, b)`.
- `"xFormat": "date"` is for daily data: axis ticks (`M/D`, a tick every whole number of days, each at the noon of a day so it sits under its point) and the tooltip heading (`Nov 22, 2026`) show the UTC date only, with no time of day, so put each point at noon UTC (`Date.UTC(y, m, d, 12)`) and the label reads the same in every time zone.
- Fetching fresh data on `pb:change` or `pb:refresh` needs `connect-src 'self'` (or your API origin) in the page's CSP.

## States

- `pbTimeRange(el)` accepts `now`, `now-15m`, `now-6h`, `now-7d`, `now-2w` and dates like `2026-11-22 03:00`, shifts by half a window and zooms out ×2.
- `pbRefresh(el)` shows a progress line on the button and pauses in background tabs.

## Keys

- Chart: `pbTimeSeries(el)` draws lines with a crosshair tooltip for every series, legend toggles (Alt-click shows only one), hover-to-focus, drag-to-zoom, ← → reading on focus and Esc to reset.

## Events

- `pb:change` from the picker with `{from, to, fromMs, toMs, label}`.
- `pb:refresh` from the refresh group with `{interval}`.
- `pb:zoom` from the chart with `{fromMs, toMs}` (or `null` on reset).

## Do and don't

- Do: wire them: on `pb:change` or `pb:refresh`, fetch and `pbSetData`; on `pb:zoom`, set the picker to the zoomed range.
