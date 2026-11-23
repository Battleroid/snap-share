Table tools add the controls people expect on big tables: columns, resizing, density, expandable rows, inline editing, CSV and a sticky first column.

- Markup: `.pb-tablewrap.pb-dt[data-dt][data-name]` with a toolbar (`[data-dt-search]`, `[data-dt-count]`, a `[data-dt-density]` radiogroup `comfortable|compact`, `[data-dt-cols]` with an empty `pb-menu.pb-dt__cols`, `[data-dt-export]`), an empty `pb-tablescroll` and a `<script type="application/json">` of `{columns, rows}`.
- Columns: `{key, label, type: "status"|"tags", num, mono, unit, editable, sticky, width, hidden, wrap, sort: false}`. Rows are objects; a `detail` object adds an expand button that shows it as a DetailList.
- `pbDataTable(el)` renders and sorts; drag a header's right edge (or focus it and use ← →) to resize; double-click or press Enter on an editable cell to edit, Enter saves, Esc cancels; it fires `edit` with `{row, key, value, old}`. Columns can be hidden (the sticky one stays); CSV exports the visible columns and filtered rows and fires `export`.
- `data-density="compact"` on any `pb-dt` tightens rows to 32px. Use compact for dense admin tables, comfortable elsewhere.
- Expanded rows show `detail` as a compact grid of label-over-value pairs (`pb-dl--grid`), at most 720px wide so values stay near their labels.
