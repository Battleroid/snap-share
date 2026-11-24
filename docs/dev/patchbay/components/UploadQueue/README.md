The upload queue shows each file's progress and lets people retry, cancel or remove it. Your code does the transfer; the queue only shows it and reports clicks.

## Markup

- `section.pb-upq[data-upq]` with `pb-upq__head` (`p.pb-upq__title[role=status]`, `[data-upq-toggle]` to minimize), `pb-upq__total`, an empty `ul.pb-upq__list` and `pb-upq__foot` (`[data-upq-pause]`, `[data-upq-cancel-all]`, `[data-upq-clear]`). `pb-upq--dock` pins it to the bottom-right corner.
- `pbUploadQueue(el)`; then `var it = el.pbAdd({name, size})` and drive it: `it.start()`, `it.progress(bytes, bytesPerSecond)`, `it.process()` (bytes are in, the server is working), `it.done()`, `it.fail(reason)`, `it.remove()`. `el.pbItems()`, `el.pbActive()` and `el.pbPaused()` read state.
- Pickers: a file input inside a `label.pb-btn` must stay keyboard reachable: give the input `class="pb-sr"` (not `hidden`); the button shows a focus ring through `:has(input:focus-visible)`.
- Controls: `data-upq-controls="cancel pause"` is the default. Set `"cancel"`, `"pause"` or `"none"` when your uploader cannot abort or pause.
- Words come from `pbStrings` (`upq*`, `pauseAll`, `resumeAll`). The transfer itself needs `connect-src` for the upload endpoint.

## States

- Queued, uploading (shows %, size, speed and time left), processing (indeterminate bar, a still chip icon, not the Retry arrows), done, failed (the reason is the row text), paused, canceled.
- The title states what is true: "Uploading 2 of 3 · 40%" while bytes move, "Processing 2 · 1 done" when only the server is working, then "3 uploaded"; with several files "12 MB of 56 MB".
- Row actions: Cancel while pending, Retry on failed or canceled rows, Remove on finished ones; `[data-upq-cancel-all]` cancels everything pending.

## Keys

- The title is `role="status"`. The label of each row button names the file.
- While anything is queued, uploading or processing, leaving the page asks first (`pbGuardUnload`).

## Events

- Bubbling, `detail` is the item: `pb:cancel` (once per item, also for cancel all; abort the request), `pb:retry` (the item is back to queued: start it again), `pb:remove` (forget it), plus `pb:pause` and `pb:resume`.

## Do and don't

- Do: abort the request on `pb:cancel` and restart it on `pb:retry`.
- Don't: offer pause or cancel when your uploader cannot honor them; set `data-upq-controls`.
