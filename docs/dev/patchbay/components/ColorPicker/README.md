ColorPicker offers kit colors as swatches, plus a hex field, used instead of the browser's own color dialog, which pages cannot style.

- Fixed choices (label or tag colors): `div.pb-swatches[role=radiogroup]` of `button.pb-swatch[role=radio]` with `style="--c: var(--token)"`, `aria-checked` and an `aria-label` naming the color. Arrow keys move the choice.
- Any color: `.pb-input[data-colorfield]` holding `button.pb-input__swatch` (shows the color, opens the popover) and a hex `<input>`. The popover (`.pb-colorpop`) lists kit colors and a custom swatch that opens the native color picker.
- Script: the preview carries it; hex input is validated (`#rrggbb`, a bare `rrggbb` is accepted).
- Always name colors in `aria-label`; never rely on the swatch alone.
