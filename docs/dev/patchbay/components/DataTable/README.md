DataTable is a native `<table class="pb-table">` in a `.pb-tablewrap` with a toolbar above and a footer below.

- Consumer provides: `.pb-tablewrap` > `.pb-toolbar` (search, Segmented filter, bulk actions) > `.pb-tablescroll` > `table.pb-table` > `.pb-table__foot` (range text and a ButtonGroup pager).
- Columns: names in `.pb-table__name` (serif), hosts and IDs in `.pb-mono`, numbers in `.pb-num` (right aligned, tabular), status as a Badge, a `.pb-cell-check` column of `pb-check`s, a `.pb-cell-actions` column with a `more` icon button opening a Menu.
- Sorting: `aria-sort="ascending|descending|none"` on the `th` and a `button.pb-th-sort` inside; the icon follows `aria-sort`.
- Selection: set `aria-selected="true"` on checked rows; the header checkbox goes indeterminate on a partial selection; show "N selected" in the toolbar.
- `pb-table--compact` for long lists. Use `—` for empty cells and `.pb-table__empty` for an empty table.
- Narrow containers: `.pb-tablescroll[data-stack]` lays each row out as a card under 560 px of its own width, every cell under its column name (the `scroll` hook copies the header text into `data-label`, also for rows added later). The header row is hidden there, so sort buttons and select-all are not available in the stacked layout; offer sorting elsewhere if a phone needs it.
- Row menus are named per row: `aria-label="More for nas-01"`, not "Actions".
