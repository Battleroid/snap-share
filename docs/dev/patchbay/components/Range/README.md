Range is a native `<input type="range" class="pb-range">` with a filled track and an `output` for its value.

- Consumer provides: the input inside a `div.pb-field` with a `label.pb-field__label[for]`, an `output.pb-output` beside the label, and a script that sets `--pct` (0–100%) on the input and the output text on `input`.
- Ticks: a `datalist` bound with `list`, mirrored by `.pb-range-ticks` labels.
- Use for values where the rough position matters more than the exact number (fan speed, retention). For exact values, use a number input.
