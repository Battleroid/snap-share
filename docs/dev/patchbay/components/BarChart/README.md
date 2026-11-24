Bar charts compare amounts across a few categories, stacked by series.

## Markup

- `figure.pb-bars[data-bars]` with a `figcaption.pb-eyebrow` and a `<script type="application/json">` of `{series: [{name, c}], cats: [...], values: [[...per cat] per series], unit, h, labels}`.
- `pbBars(el)` draws stacked columns on a rounded scale with gridlines and a legend; `labels` caps how many x labels show. Colors from `chart-*` tokens, up to four series.
- The chart builds its own markup with `data-style` and applies it through `pbStyled`, so it runs under `style-src 'self'`. The JSON block is data, not script.

## States

- `pb-bars--h` draws horizontal stacked bars with the total at the end: top clients, biggest folders. Use it when labels are long.
- The y axis widens to its longest label.

## Keys

Vertical bars: the columns are one tab stop; ←/→ (and ↑/↓), Home and End move between them. Each column is a button whose name is its category and values, and whose tooltip lists every series and the total.

## Events

None; the component fires no events of its own.

## Do and don't

- Do: use it for a few categories compared across up to four series.
- Don't: use it for continuous time series; use Chart.
