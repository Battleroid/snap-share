Charts are plain SVG styled by class: series colors come from `chart-1…4`, text from `ink-muted` in Obel Mono.

- Consumer provides: `.pb-chart` with an eyebrow title (metric · window · unit) and an `<svg>`; draw with any library or by hand.
- Classes: `pb-grid` gridlines, `pb-axis` labels, `pb-line` + `pb-s1…s4` series, `pb-dashed` for baselines (`chart-3`), `pb-area` under the first series, `pb-bar` bars (corner from the edge variant).
- `pb-legend` under the chart; `<i style="--c:var(--chart-1)">` swatches.
- `pb-stat` is a label, a `metric` value with a smaller unit, and a `pb-stat__delta` with `data-tone="good|bad"`. `pb-meter` is a capacity bar (`--value`, `data-status="warn|down"` past thresholds).
- At most four series. `chart-2` and `chart-4` are for marks, never text.
- Hover: call `pbChartHover(svg, spec)` after drawing (in `patchbay.js`). It adds a crosshair and dots for lines, or a highlighted column or row for bars (`band`), and a tooltip with every series' value. Arrow keys step through points when the chart has focus.
  - `spec`: `pos` (x of each point in viewBox units, or y with `axis: 'y'` for horizontal bars), `labels` (tooltip title per point), `series` (`name`, `c` color or one color per point, `v` values, `at` [x, y] dot positions, `unit`), `top` / `bottom` (crosshair extent), `band` (column or row width).
