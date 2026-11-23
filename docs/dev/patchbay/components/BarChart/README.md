Bar charts compare amounts across a few categories, stacked by series.

- Markup: `figure.pb-bars[data-bars]` with a `figcaption.pb-eyebrow` and a `<script type="application/json">` of `{series: [{name, c}], cats: [...], values: [[...per cat] per series], unit, h, labels}`.
- `pbBars(el)` draws stacked columns on a rounded scale with gridlines and a legend; each column is a focusable button whose tooltip lists every series and the total. `labels` caps how many x labels show.
- `pb-bars--h` draws horizontal stacked bars with the total at the end: top clients, biggest folders. Use it when labels are long.
- Up to four series; colors from `chart-*` tokens.
