Range is a native `<input type="range" class="pb-range">` with a filled track and an `output` for its value.

## Markup
- The input inside a `div.pb-field` with a `label.pb-field__label[for]` and an `output.pb-output` beside the label. No script of your own: the `range` hook sets `--pct` (the filled share, 0–100%) on the input and the `output` text on `input`.
- Ticks: a `datalist` bound with `list`, mirrored by `.pb-range-ticks` labels. Ticks sit at their value (the span's text, or `data-v`) under the thumb, not spread evenly.
- A `data-unit` on the input adds a unit to the spoken value.

## States
- The track fills to the thumb through `--pct`. The output ends in `%` when its text already contains `%`.

## Keys
- Native slider: arrows, Home, End, PageUp/PageDown.
- The `label[for]` names the slider; the hook marks the `output` beside it `aria-hidden` (it repeats the value) and sets `aria-valuetext` ("60%", plus `data-unit` when set).

## Events
None of its own; the native `input` and `change` from the range.

## Do and don't
- Do: use for values where the rough position matters more than the exact number (fan speed, retention).
- Don't: use it for exact values; use a number input.
- Don't: write your own `--pct` script.
