The image viewer shows a gallery grid and opens any image full screen.

- Markup: `ul.pb-gallery[data-lightbox] > li > a[href=full image] > img[src=thumb][alt]`. Optional `data-name` (file name shown and used for download) and `data-caption` on the link.
- `pbLightbox(el)` adds a `dialog.pb-lightbox` on the `night` ground with a counter, name, zoom, download, close, previous and next, caption and a thumbnail strip.
- Keys: ←/→ move, Z zooms (click the image too), Esc closes. Swipe on touch. Without the script the links still open the full image.
- Thumbnails are cropped square-ish (4:3); the viewer shows the whole image. Always set `alt`.
- Members: `[data-lightbox]` collects `a[data-lightbox-item]` when any are present (else every `a[href]`, as before); `data-lightbox="selector"` picks them with your own selector. Images show in the stage; links to video or audio (by extension, or `data-kind="video|audio"`, with an optional `data-poster`) show a MediaPlayer there instead, bound with `pbInit()`, and stop when you move on or close. Zoom is for images only.
- Members are read each time the viewer opens, so tiles can be added, removed or reordered (htmx swaps, drag to sort) without binding again. `gallery.pbOpen(i)` opens it at a member from script.
- Closing returns focus to the link that opened it, unless your code moved focus elsewhere first.
- Ctrl/Cmd-, Shift- and Alt-click (and middle-click) keep the link's own behavior, such as opening the image in a new tab.
