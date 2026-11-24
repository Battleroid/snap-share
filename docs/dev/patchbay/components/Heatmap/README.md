The calendar heatmap shows a value per day across weeks: activity, or pass/fail.

## Markup

- `.pb-heat[data-heat]` with a `<script type="application/json">` of `{end: "YYYY-MM-DD", values: [...], unit, label}`. Values run oldest first.
- `pbHeat(el)` draws weeks as columns (Sunday on top), month and weekday labels, a legend and a readout.

## States

- Numbers make a five-step accent scale (quartiles); `"ok"|"warn"|"down"` make a status calendar; `null` means no data.
- It scrolls sideways on phones and starts at the newest week. The scroll box is named and marked with `data-more-start` / `data-more-end` for the edge fade when the grid is wider than the page.

## Keys

- The grid is one tab stop. ←/→ move by week and ↑/↓ by day; hovering or moving reads each day aloud in the readout.

## Events

- None; the component fires no events of its own.

## Do and don't

- Do: keep it to a year at most.
