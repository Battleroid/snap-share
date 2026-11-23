The calendar heatmap shows a value per day across weeks: activity, or pass/fail.

- Markup: `.pb-heat[data-heat]` with a `<script type="application/json">` of `{end: "YYYY-MM-DD", values: [...], unit, label}`. Values run oldest first; numbers make a five-step accent scale (quartiles), `"ok"|"warn"|"down"` make a status calendar, `null` means no data.
- `pbHeat(el)` draws weeks as columns (Sunday on top), month and weekday labels, a legend and a readout. Hovering or using the arrow keys on the focused grid reads each day aloud.
- Keep it to a year at most; it scrolls sideways on phones and starts at the newest week.
