Table tools add the controls people expect on big tables: columns, resizing, density, expandable rows, inline editing, CSV and a sticky first column.

## Markup

- `.pb-tablewrap.pb-dt[data-dt][data-name]` with a toolbar (`[data-dt-search]`, `[data-dt-count]`, a `[data-dt-density]` radiogroup `comfortable|compact`, `[data-dt-cols]` with an empty `pb-menu.pb-dt__cols`, `[data-dt-export]`), an empty `pb-tablescroll` and a `<script type="application/json">` of `{columns, rows}`.
- Columns: `{key, label, type: "status"|"tags", num, mono, unit, editable, sticky, width, hidden, wrap, sort: false}`. Rows are objects; a `detail` object adds an expand button that shows it as a DetailList.
- Resize handle: `span.pb-dt__resize[role=separator][aria-orientation=vertical]` with `aria-valuenow` (px), `aria-valuemin="60"` and `aria-valuemax="800"`, and a 24 px hit area. A set width is the column's width, not only a cap (`width`, `min-width` and `max-width` from `--w`).
- `pbDataTable(el)` renders and sorts.

## States

- `data-density="compact"` on any `pb-dt` tightens rows to 32px (header 30px). Use compact for dense admin tables, comfortable elsewhere.
- Expanded rows show `detail` as a compact grid of label-over-value pairs (`pb-dl--grid`), at most 720px wide so values stay near their labels.
- Columns can be hidden (the sticky one stays); CSV exports the visible columns and filtered rows.

## Keys

- Keyboard (`pbGridKeys`): the table is `role=grid` and one tab stop. ←/→ move between cells (and the sort button and resize handle in a header), ↑/↓ to the same column, Home/End to the row's ends, Ctrl+Home/End to the grid's, PageUp/PageDown by ten rows. Enter on a header sorts.
- Resize: drag a header's right edge, or focus the handle and press ←/→ to change the width by 16 px. The code enforces a 60 px minimum by drag and keyboard; the 800 value is only what `aria-valuemax` reports, it is not clamped. Dragging never selects text.
- Editing: double-click, or Enter or F2 on an editable cell; Enter saves, Esc cancels and focus stays on the cell.

## Events

- `pb:edit` with `{row, key, value, old}` after a changed edit is saved.
- `pb:export` with the CSV text when CSV is exported.

## Do and don't

- Do: use compact for dense admin tables.
- Don't: rely on a cap alone for column width; a set width is the width.
