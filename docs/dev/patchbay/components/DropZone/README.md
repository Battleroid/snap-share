DropZone accepts files by drag or browse and lists uploads under it.

- Consumer provides: `<label class="pb-drop">` with a hidden `<input type="file">`, `.pb-drop__icon` (`pb-icon--upload`), `.pb-drop__title`, `.pb-drop__hint` (accepted types and size limit, with `<u>browse</u>`).
- State via `data-state`: none = idle, `over` while a drag is over it (swap the icon to `download`, title to "Release to upload N files"), `error` for a rejected drop (say why in the hint).
- Uploads: `.pb-filelist` > `.pb-filerow` (icon, name, `.pb-filerow__meta`), with a `.pb-progress` bar (`--value: 62%`) while sending; swap the icon to `status-ok` in `ok` when done.
