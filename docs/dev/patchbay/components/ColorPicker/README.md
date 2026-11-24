ColorPicker offers kit colors as swatches, plus a hex field, used instead of the browser's own color dialog, which pages cannot style.

## Markup

- Fixed choices (label or tag colors): `div.pb-swatches[role=radiogroup][data-swatch-group]` of `button.pb-swatch[role=radio]` with `data-vars="--c:var(--token)"` (no `style` attribute: the kit runs under `style-src 'self'`; the `vars` hook sets the property through the CSSOM), `aria-checked` and an `aria-label` naming the color. For markup built as a string use `data-style="--c:var(--token)"` and `pbStyled(container)`.
- Any color: `.pb-input[data-colorfield]` holding `button.pb-input__swatch` (shows the color, opens the popover) and a hex `<input>`. The popover (`.pb-colorpop`) lists kit colors and a custom swatch that opens the native color picker. It is built by the `colorfield` hook.
- `data-swatch-group` and `data-colorfield` are bound by `pbInit`; no script of your own is needed.

## States

- The hex input is validated (`#rrggbb`, a bare `rrggbb` is accepted); a non-empty value that is not a hex color marks the field invalid (`data-invalid`).
- An empty color field is valid (no color chosen) unless its input is `required`.

## Keys

Swatch groups: one tab stop, arrow keys move the choice. The swatch button opens the popover and focuses the checked kit color (or the first); choosing a color closes it and returns focus to the swatch button.

## Events

Setting a color writes the hex into the input, so the input's native `change` fires when the user types; swatch choices set `aria-checked`. The component fires no `pb:` events.

## Do and don't

- Do: always name colors in `aria-label`.
- Don't: rely on the swatch alone.
- Don't: write inline `style="--c:…"`; use `data-vars`.
