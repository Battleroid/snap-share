DataTable is a native `<table class="pb-table">` in a `.pb-tablewrap` with a toolbar above and a footer below.

## Markup

- Consumer provides: `.pb-tablewrap` > `.pb-toolbar` (search, Segmented filter, bulk actions) > `.pb-tablescroll` > `table.pb-table` > `.pb-table__foot` (range text and a ButtonGroup pager).
- Columns: names in `.pb-table__name` (serif), hosts and IDs in `.pb-mono`, numbers in `.pb-num` (right aligned, tabular), status as a Badge, a `.pb-cell-check` column of `pb-check`s, a `.pb-cell-actions` column with a `more` icon button opening a Menu.
- Sorting: `aria-sort="ascending|descending|none"` on the `th` and a `button.pb-th-sort` directly inside; the icon follows `aria-sort`. The built-in sort reorders the rows by cell text (numerically when both cells parse as numbers).
- Selection: a `data-all` checkbox in the header, a `pb-check` per row and a `[data-selcount]` element in the `.pb-tablewrap` turn on the built-in selection; without them nothing is wired.
- `pb-table--compact` for long lists. Use `—` for empty cells and `.pb-table__empty` for an empty table.
- Row menus are named per row: `aria-label="More for nas-01"`, not "Actions".

## States

- Sorted column: `aria-sort` on the `th`; the others are `none`.
- Selected rows carry `aria-selected="true"`; the header checkbox goes indeterminate on a partial selection and "N selected" shows in `[data-selcount]`.
- Narrow containers: `.pb-tablescroll[data-stack]` lays each row out as a card under 560 px of its own width, every cell under its column name (the `scroll` hook copies the header text into `data-label`, also for rows added later). The header row is hidden there, so sort buttons and select-all are not available in the stacked layout; offer sorting elsewhere if a phone needs it.

## Keys

- Native controls only: Tab and Space/Enter reach the sort buttons, row checkboxes and row menus as for the elements.
- A sort button is a real `button`; the column's `aria-sort` carries the order for screen readers.

## Events

- `pb:sort` bubbles from the table with `detail: {column, direction}` (column index, `ascending` or `descending`) after the rows are reordered.
- Checkboxes fire the native `change`.

## Do and don't

- Do: name every row menu after its row.
- Do: use `—` for an empty cell.
- Don't: rely on sort or select-all in the stacked layout; the header is hidden there.
