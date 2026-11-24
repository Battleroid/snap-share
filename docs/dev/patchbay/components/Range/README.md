Range is a native `<input type="range" class="pb-range">` with a filled track and an `output` for its value.

- Consumer provides: the input inside a `div.pb-field` with a `label.pb-field__label[for]`, an `output.pb-output` beside the label, and a script that sets `--pct` (0–100%) on the input and the output text on `input`.
- Ticks: a `datalist` bound with `list`, mirrored by `.pb-range-ticks` labels.
- Use for values where the rough position matters more than the exact number (fan speed, retention). For exact values, use a number input.
- Names and values: the `label[for]` names the slider; the hook marks the `output` beside it `aria-hidden` (it repeats the value) and sets `aria-valuetext` ("60%", plus `data-unit` when set). It also sets `--pct`. Ticks in `.pb-range-ticks` sit at their value (the span's text, or `data-v`) under the thumb, not spread evenly.
