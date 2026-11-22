Sparklines are small trend lines beside a number, sized to sit in a row of text.

- Consumer provides: `<svg class="pb-spark">` with a `<path>` of the series (draw it at the svg's own pixel size). Optional `path.pb-spark__area` under it, `path.pb-spark__base` for a baseline, a `circle` on the last point, or `rect`s for bars.
- Sizes: default 96×28, `pb-spark--sm` 64×20, `pb-spark--lg` 160×40. Color: `chart-1` by default, or `pb-spark--ok`, `--warn`, `--down`, `--muted`.
- With a metric: `.pb-metric` > `.pb-metric__value` (unit in `small`) and the sparkline, or `.pb-metric__side` holding the sparkline over a `pb-stat__delta`. `pb-metric--sm` for inline use with an eyebrow.
- In lists: `.pb-metrics` > `.pb-metrics__row` (name and host, sparkline, `.pb-metrics__val`, Badge).
- A sparkline shows shape, not values: always print the current number beside it.
