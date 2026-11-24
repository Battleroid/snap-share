DualRange is a slider with two handles for a minimum and maximum.

## Markup

- Consumer provides: `div.pb-range2[role=group][data-range2]` with an `aria-labelledby` label and two `input.pb-range` (same `min`, `max`, `step`; each with its own `aria-label`), an `output.pb-output[data-out]` for the formatted range in the same `.pb-field`, and optional `.pb-range-ticks`.
- `data-unit="size"` maps 0 to 100 onto a log scale of file sizes ("10 MB – 2 GB"); `data-unit="hour"` shows hours ("08:00 – 22:00"). Any other value falls back to sizes.
- The `range2` hook keeps the low handle at or below the high one, paints the track between them via `--lo` / `--hi`, and writes the output.

## States

- The two handles cannot cross: dragging one past the other pushes it back.

## Keys

- Each thumb is a native range input: arrows, Home, End and PageUp/PageDown change it. Each gets `aria-valuetext` in the shown unit ("40 KB", "08:00"), and the combined `output` is `aria-hidden`.

## Events

- The native `input` and `change` of each range input; the component fires no `pb:` events.

## Do and don't

- Do: use a log scale for sizes.
- Do: always print both values as text.
