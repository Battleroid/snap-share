Virtual lists scroll through tens of thousands of rows while only drawing the visible ones.

- Markup: an empty `.pb-vlist[aria-label]`; `--vh` sets the height and `--rh` the row height (default 32px, all rows the same height).
- `pbVirtual(el, {count, render(i), onRange(a, b)})` renders `.pb-vlist__row` items with `aria-setsize` and `aria-posinset`. Arrows, Page Up/Down, Home and End move the active row; `el.pbScrollTo(i, center)` jumps; `el.pbSetCount(n)` updates the total.
- Use it past about 2,000 rows: audit logs, file indexes, long tables. Below that render normally so browser find works.
