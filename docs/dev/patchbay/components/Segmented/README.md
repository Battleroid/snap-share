Segmented controls are conjoined toggle buttons built from native radios or checkboxes.

## Markup
- `div.pb-seg` (`role="radiogroup"` and `aria-label` for radios) holding `<label><input type="radio" name="…">Text</label>` per segment. The input covers its label and is invisible.
- Single choice: radios. Multi choice (log levels, filters): checkboxes in the same markup.
- `pb-seg--icon` for icon-only segments (give each input an `aria-label`), `pb-seg--sm` for toolbars, `pb-seg--block` to fill the row.

## States
- The label fills `accent` when checked.
- `disabled` inputs are dimmed.

## Keys
- Radios: native group, Tab enters, arrows move and select. Checkboxes: Tab to each and Space toggles.

## Events
None of its own; the native `change` (and `input`) from the inputs.

## Do and don't
- Do: 2–5 segments of one or two words, for views, time windows and filters.
- Don't: use it for actions; use ButtonGroup.
- Don't: leave an icon-only segment without an `aria-label`.
