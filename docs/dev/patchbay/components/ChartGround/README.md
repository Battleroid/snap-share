Charts can sit on a ground and mark ranges and limits, in addition to the plain Chart.

- `pb-chart--panel`: a `surface-sunk` panel for a chart inside a page of white cards.
- `pb-chart--ink`: an `ink` ground; series switch to `pop-mint`, `pop-yellow`, `on-ink-muted`, `pop-red`, and labels to `on-ink-muted`. Use once per page for the chart that matters most.
- `rect.pb-band` (or `pb-band--warn`) marks a time range behind the series (maintenance, an incident), with a `text.pb-band-label`.
- `line.pb-threshold` with `text.pb-threshold-label` marks a limit (SLO, quota) in `down`, dashed.
- `circle.pb-dot-end` marks the latest point.
- Draw at the container's pixel width and redraw on resize, so text stays 11px.
- Every ground takes the same hover as Chart: `pbChartHover(svg, spec)` after each redraw. Give reference series (a limit, a quota) a value per point and no `at`, so they show in the tooltip without a dot.
