Histograms show how values are distributed, such as response times.

- Markup: `figure.pb-bars.pb-hist[data-hist]` with a `<script type="application/json">` of `{bins: [[lo, hi, count]], unit, marks: [{at, label, status}], countLabel, labels, h}`.
- `pbHist(el)` draws touching bars, one per bin, with a tooltip per bin and dashed markers for percentiles (`status: "down"` for an SLO line).
- Bins may be uneven, so each bar is the count in its bin, not an area. Say how many samples and over what time in the caption.
