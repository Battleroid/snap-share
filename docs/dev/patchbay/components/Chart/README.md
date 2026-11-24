Charts are plain SVG styled by class: series colors come from `chart-1…4`, text from `ink-muted` in Obel Mono.

## Markup

- `.pb-chart` with an eyebrow title (metric · window · unit) and an `<svg>`; draw with any library or by hand.
- Classes: `pb-grid` gridlines, `pb-axis` labels, `pb-line` + `pb-s1…s4` series, `pb-dashed` for baselines (`chart-3`), `pb-area` under the first series, `pb-bar` bars (corner from the edge variant).
- `pb-legend` under the chart. Swatches take their color from a custom property set through `data-vars`, not a `style` attribute: `<i data-vars="--c:var(--chart-1)">`. For markup built as a string, write `data-style="--c:var(--chart-1)"` and call `pbStyled(container)` after inserting it.
- `pb-stat` is a label, a `metric` value with a smaller unit, and a `pb-stat__delta` with `data-tone="good|bad"`. `pb-meter` is a capacity bar (`--value`).
- Hover: call `pbChartHover(svg, spec)` after drawing (in `patchbay.js`). It adds a crosshair and dots for lines, or a highlighted column or row for bars (`band`), and a tooltip with every series' value.
  - `spec`: `pos` (x of each point in viewBox units, or y with `axis: 'y'` for horizontal bars), `labels` (tooltip title per point), `series` (`name`, `c` color or one color per point, `v` values, `at` [x, y] dot positions, `unit`), `top` / `bottom` (crosshair extent), `band` (column or row width).

## States

- Colors: series come from `chart-1…4`; text from `ink-muted` in Obel Mono. At most four series.
- `pb-meter` takes `data-status="warn|down"` past thresholds.
- `chart-2` and `chart-4` are for marks, never text.

## Keys

Arrow keys step through points when the chart has focus. They also speak the point (its label and every series' value) through the shared polite live region (`pbAnnounce`); pointer hover stays silent.

## Events

None; the component fires no events of its own.

## Do and don't

- Do: give the chart an eyebrow title saying metric, window and unit.
- Don't: use inline `style="..."`; the kit runs under `style-src 'self'`.
- Don't: use `chart-2` or `chart-4` for text.
