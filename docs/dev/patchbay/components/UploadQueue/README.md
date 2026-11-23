The upload queue shows each file's progress and lets people retry or cancel.

- Markup: `section.pb-upq[data-upq]` with `pb-upq__head` (`p.pb-upq__title[role=status]`, `[data-upq-toggle]` to minimize), `pb-upq__total`, an empty `ul.pb-upq__list` and `pb-upq__foot` (`[data-upq-pause]`, `[data-upq-clear]`). `pb-upq--dock` pins it to the bottom-right corner.
- `pbUploadQueue(el)`; then `var it = el.pbAdd({name, size})`, `it.start()`, `it.progress(bytes, bytesPerSecond)`, `it.done()`, `it.fail(message)`. States: queued, uploading, paused, done, failed, canceled. Rows show size, speed and time left; failed rows say why and offer Retry.
- Events: `cancel` and `retry` (item), `pause` and `resume`. Your code does the uploading; the queue only shows it.
- Controls: `data-upq-controls="cancel pause"` is the default. Set `"cancel"`, `"pause"` or `"none"` when your uploader cannot abort or pause: without `cancel` in-progress rows get no Cancel button, without `pause` the `[data-upq-pause]` button stays hidden.
