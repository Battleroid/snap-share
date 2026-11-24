Select wraps a native `<select>` in `span.pb-select`, which draws the chevron.

## Markup
- `<span class="pb-select"><select>…</select></span>` inside a `div.pb-field` with a `label.pb-field__label[for]` naming the select. `optgroup` for grouped options. `pb-select--sm` for toolbars.
- Multiple: `pb-select--multi` with `<select multiple size="N">`; chosen options fill `accent`. Say how to multi-select in the help line.
- Free text with suggestions: a `.pb-input` with `<input list="id">` plus a `<datalist id="id">`, and a trailing `pb-icon--chevron-down`.
- Options are mono, lowercase where they are identifiers.

## States
- In browsers with customizable select (`appearance: base-select`), the open list is drawn in the kit's style: `surface`, the edge variant's border and shadow, mono options, a hover fill and an `accent` check on the chosen one. Other browsers show their native list. Multiple selects keep the native list.
- `disabled` and the field's invalid state follow the native control.

## Keys
- Native select: Tab, Space/Enter or arrows to open, type to jump.

## Events
None of its own; the native `change` (and `input`) from the select.

## Do and don't
- Do: name every select with a `label[for]`.
- Do: use a datalist input for free text with suggestions.
- Don't: use for 2–6 options that need hints; use Radio.
