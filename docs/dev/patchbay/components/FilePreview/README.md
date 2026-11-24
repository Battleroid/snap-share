The file preview pane shows the selected file beside the file browser.

## Markup

- `aside.pb-fpreview[data-fpreview]` with `pb-fpreview__head` (`p.pb-fpreview__name[aria-live]`, `pb-fpreview__acts`, `[data-fpreview-close]`), an empty `pb-fpreview__body` and `pb-fpreview__meta`. Put it in the second pane of a Resizable split next to a FileBrowser.
- `pbFilePreview(pane)` (the `fpreview` hook) then `pane.pbShow({name, kind, size, date, src, poster, text, lang, pages, count, owner, path, sha, type})`; `pbShow(null)` shows the empty state.
- Details below the preview: type, size, modified, owner, path, checksum.
- CSP: image and video `src` values load from their own origins, so `img-src` and `media-src` must allow them.

## States

- Kinds: `image` (contain on a checkerboard), `video` (MediaPlayer), `text` (line numbers; `lang: "yaml"` highlights), `pdf` (first page and Open PDF), `folder`, anything else (no preview, download it).
- Wire it to the browser's `pb:select` event: one row previews it, several rows show "N items selected", none shows the empty state.

## Keys

- The file name is a polite live region, so a new selection is announced. Text previews are focusable and named "<name> contents".
- The close button is a native button.

## Events

- Fires `pb:close` (bubbling, no detail) on the pane when `[data-fpreview-close]` is pressed, after resetting to the empty state.
- Listens for nothing; call `pbShow` from the FileBrowser's `pb:select`.

## Do and don't

- Do: show "N items selected" for a multi-selection instead of previewing one file.
- Don't: expect previews for kinds it does not know; those say there is no preview.
