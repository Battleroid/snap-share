DualRange is a slider with two handles for a minimum and maximum.

- Consumer provides: `div.pb-range2[role=group]` with an `aria-labelledby` label and two `input.pb-range` (same `min`, `max`, `step`; each with its own `aria-label`), an `output.pb-output` for the formatted range, and optional `.pb-range-ticks`.
- Script: keeps the low handle at or below the high one, paints the track between them via `--lo` / `--hi`, and writes the output ("10 MB – 2 GB").
- Use a log scale for sizes; always print both values as text.
- Each thumb gets `aria-valuetext` in the shown unit ("40 KB", "08:00"), and the combined `output` is `aria-hidden`.
