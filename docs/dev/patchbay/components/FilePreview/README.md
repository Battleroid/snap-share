The file preview pane shows the selected file beside the file browser.

- Markup: `aside.pb-fpreview[data-fpreview]` with `pb-fpreview__head` (`p.pb-fpreview__name[aria-live]`, `pb-fpreview__acts`, `[data-fpreview-close]`), an empty `pb-fpreview__body` and `pb-fpreview__meta`. Put it in the second pane of a Resizable split next to a FileBrowser.
- `pbFilePreview(pane)` then `pane.pbShow({name, kind, size, date, src, poster, text, lang, pages, count, owner, path, sha, type})`; `pbShow(null)` shows the empty state. Kinds: `image` (contain on a checkerboard), `video` (MediaPlayer), `text` (line numbers; `lang: "yaml"` highlights), `pdf` (first page and Open PDF), `folder`, anything else (no preview, download it).
- Wire it to the browser's `pb:select` event: one row previews it, several rows show "N items selected", none shows the empty state.
- Details below the preview: type, size, modified, owner, path, checksum.
