A compact usage ring for app headers, with a breakdown in a popover.

- Markup: `<button type="button" class="pb-ring" data-ring data-value="62" popovertarget="id" aria-label="Storage {n}% used">` plus a `.pb-popover.pb-ring__pop[popover]` with the breakdown (`pb-ring__head`, a `progress`, `pb-ring__line`, `pb-ring__more`).
- `data-value` is a percent, or give `data-used` and `data-total`. `data-warn` (default 80) and `data-down` (default 95) set `data-level`, which colours the arc. `{n}` in the label becomes the rounded percent; `el.pbSet(percent)` redraws.
- The ring is never the only signal: the popover states the numbers and the label states the percent. The button is 32 px.
- `data-show-value` adds the percent as visible text next to the ring (the label still names it, e.g. "Storage 62% used"). Put it in the app header beside the account menu.
