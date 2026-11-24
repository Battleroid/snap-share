The file browser lists a folder's files and folders with selection, sorting and drop to upload.

## Markup

- `.pb-files[data-files]` with a toolbar (path breadcrumbs, `[data-files-filter]`, a `[data-files-view]` list/grid radiogroup, actions), `pb-files__body` holding `pb-files__head` (sort buttons `data-sort="name|size|date"`) and `pb-files__list` of `.pb-files__row[data-kind=folder|image|file][data-size][data-date]` (`pb-files__icon` with an icon or a thumbnail `img`, `pb-files__name`, `pb-files__size`, `pb-files__date`), `pb-files__foot` with `[data-files-count]`, and `pb-files__drop`.
- `pbFiles(el)` (the `files` hook) adds selection, sorting with folders first, filtering, the list/grid switch and the drop overlay.
- Pair with BulkActions for actions on the selection and UploadQueue for drops.

## States

- List or grid view (`pb-files--grid`); grid view is for photo folders.
- Rows are `aria-selected` true or false. The header sort buttons show the order with `data-sort-dir` and say it in their name ("Name, sorted ascending").
- A filter that hides every row shows a status line ("Nothing matches “x”.") under the list.
- Dragging files over the browser sets `data-dragover` and shows the drop overlay.

## Keys

- Selection: click, Shift-click ranges, ⌘/Ctrl-click, arrows, Space (toggle), ⌘/Ctrl+A, Esc (clear). Shift+arrows extend the selection. Home and End jump to the first and last row; in grid view ←/→ move by tile and ↑/↓ by row. Enter opens the row.
- The list is a multi-select `listbox` of `option` rows (`aria-selected`), in both list and grid views, with one tab stop.
- A scripted click (a preview picking its first file) selects without moving focus or scrolling.

## Events

- `pb:select` (detail: the selected rows), `pb:open` (detail: the row, on Enter or double-click), `pb:drop-files` (detail: the `FileList`).

## Do and don't

- Do: use list view everywhere except photo folders.
- Don't: upload from the browser itself; hand the dropped `FileList` to your upload code or UploadQueue.
