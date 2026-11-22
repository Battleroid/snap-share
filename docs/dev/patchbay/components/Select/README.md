Select wraps a native `<select>` in `span.pb-select`, which draws the chevron.

- Consumer provides: `<span class="pb-select"><select>…</select></span>` inside a `.pb-field`. `optgroup` for grouped options. `pb-select--sm` for toolbars.
- Multiple: `pb-select--multi` with `<select multiple size="N">`; chosen options fill `accent`. Say how to multi-select in the help line.
- Free text with suggestions: a `.pb-input` with `<input list="id">` plus a `<datalist id="id">`, and a trailing `pb-icon--chevron-down`.
- Options are mono, lowercase where they are identifiers.
- In browsers with customizable select (`appearance: base-select`), the open list is drawn in the kit's style: `surface`, the edge variant's border and shadow, mono options, a hover fill and an `accent` check on the chosen one. Other browsers show their native list. Multiple selects keep the native list.
