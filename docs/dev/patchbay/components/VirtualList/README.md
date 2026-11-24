Virtual lists scroll through tens of thousands of rows while only drawing the visible ones.

- Markup: an empty `.pb-vlist[aria-label]`; `--vh` sets the height and `--rh` the row height (default 32px, all rows the same height).
- `pbVirtual(el, {count, render(i), onRange(a, b)})` renders `.pb-vlist__row` options in a `listbox` with `id`, `aria-setsize`, `aria-posinset` and `aria-selected`; the list's `aria-activedescendant` names the active row, so End to row 10,000 is announced. Arrows, Page Up/Down, Home and End move the active row; a click or Enter fires `pb:select` with `{index}`; `el.pbScrollTo(i, center)` jumps; `el.pbSetCount(n)` updates the total.
- Use it past about 2,000 rows: audit logs, file indexes, long tables. Below that render normally so browser find works.
