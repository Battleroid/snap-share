The image viewer shows a gallery grid and opens any image full screen.

## Markup

- `ul.pb-gallery[data-imageviewer] > li > a[href=full image] > img[src=thumb][alt]`. Optional `data-name` (file name shown and used for download) and `data-caption` on the link.
- `pbLightbox(el)` (the `imageviewer` hook) adds a `dialog.pb-lightbox` on the `night` ground with a counter, name, zoom, download, close, previous and next, caption and a thumbnail strip.
- Members: `[data-imageviewer]` collects `a[data-imageviewer-item]` when any are present (else every `a[href]`); `data-imageviewer="selector"` picks them with your own selector. Members are read each time the viewer opens, so tiles can be added, removed or reordered (htmx swaps, drag to sort) without binding again. `gallery.pbOpen(i)` opens it at a member from script.
- Images show in the stage; links to video or audio (by extension, or `data-kind="video|audio"`, with an optional `data-poster`) show a MediaPlayer there instead, bound with `pbInit()`, and stop when you move on or close.
- CSP: thumbnails, full images and media load from their own origins, so `img-src` and `media-src` must allow them.

## States

- Zoom is for images only. Thumbnails are cropped square-ish (4:3); the viewer shows the whole image.
- Without the script the links still open the full image.
- Ctrl/Cmd-, Shift- and Alt-click (and middle-click) keep the link's own behavior, such as opening the image in a new tab.

## Keys

- ←/→ move, Home and End jump to the first and last, Z zooms (click the image too), Esc closes. Swipe on touch.
- Opening focuses the close button. Closing returns focus to the link that opened it, unless your code moved focus elsewhere first.

## Events

- None of its own; `pbOpen(i)` is the script entry. The lightbox is a native `dialog`, so `close` fires on it.

## Do and don't

- Do: always set `alt`.
