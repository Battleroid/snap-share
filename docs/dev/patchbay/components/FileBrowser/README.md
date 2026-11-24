The file browser lists a folder's files and folders with selection, sorting and drop to upload.

- Markup: `.pb-files[data-files]` with a toolbar (path breadcrumbs, `[data-files-filter]`, a `[data-files-view]` list/grid radiogroup, actions), `pb-files__body` holding `pb-files__head` (sort buttons `data-sort="name|size|date"`) and `pb-files__list` of `.pb-files__row[data-kind=folder|image|file][data-size][data-date]` (`pb-files__icon` with an icon or a thumbnail `img`, `pb-files__name`, `pb-files__size`, `pb-files__date`), `pb-files__foot` with `[data-files-count]`, and `pb-files__drop`.
- `pbFiles(el)` adds selection (click, Shift-click ranges, ⌘/Ctrl-click, arrows, Space, ⌘A, Esc), sorting with folders first, filtering, the list/grid switch and the drop overlay. Events: `pb:select` (rows), `pb:open` (row, on Enter or double-click), `pb:drop-files` (FileList).
- Pair with BulkActions for actions on the selection and UploadQueue for drops.
- Grid view is for photo folders; list view everywhere else.
- Semantics: the list is a multi-select `listbox` of `option` rows (`aria-selected`), in both list and grid views. The header sort buttons show the order with `data-sort-dir` and say it in their name ("Name, sorted ascending").
