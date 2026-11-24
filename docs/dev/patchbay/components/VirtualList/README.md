Virtual lists scroll through tens of thousands of rows while only drawing the visible ones.

## Markup

- An empty `.pb-vlist[aria-label]`; `--vh` sets the height and `--rh` the row height (default 32px, all rows the same height).
- `pbVirtual(el, {count, render(i), onRange(a, b)})` renders `.pb-vlist__row` options in a `listbox` with `id`, `aria-setsize`, `aria-posinset` and `aria-selected`. `el.pbScrollTo(i, center)` jumps; `el.pbSetCount(n)` updates the total.
- If rows come from a fetch, the CSP needs `connect-src 'self'` (or your API origin).

## States

- All rows must be the same height.

## Keys

- The list's `aria-activedescendant` names the active row, so End to row 10,000 is announced. Arrows, Page Up/Down, Home and End move the active row; Enter selects it.

## Events

- `pb:select` with `{index}` on a click or Enter.

## Do and don't

- Do: use it past about 2,000 rows: audit logs, file indexes, long tables.
- Don't: use it below that; render normally so browser find works.
