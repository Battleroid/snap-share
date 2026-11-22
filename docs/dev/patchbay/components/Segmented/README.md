Segmented controls are conjoined toggle buttons built from native radios or checkboxes.

- Consumer provides: `div.pb-seg` (`role="radiogroup"` and `aria-label` for radios) holding `<label><input type="radio" name="…">Text</label>` per segment. The input covers its label and is invisible; the label fills `accent` when checked.
- Single choice: radios. Multi choice (log levels, filters): checkboxes in the same markup.
- `pb-seg--icon` for icon-only segments (give each input an `aria-label`), `pb-seg--sm` for toolbars, `pb-seg--block` to fill the row.
- 2–5 segments of one or two words. Use for views, time windows, filters; for actions, use ButtonGroup.
