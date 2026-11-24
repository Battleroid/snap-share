A compact usage ring for app headers, with a breakdown in a popover.

## Markup

- `<button type="button" class="pb-ring" data-ring data-value="62" popovertarget="id" aria-label="Storage {n}% used">` plus a `.pb-popover.pb-ring__pop[popover]` with the breakdown (`pb-ring__head`, a `progress`, `pb-ring__line`, `pb-ring__more`).
- `data-value` is a percent, or give `data-used` and `data-total`. `{n}` in the label becomes the rounded percent; `el.pbSet(percent)` redraws.
- `data-show-value` adds the percent as visible text next to the ring (the label still names it, e.g. "Storage 62% used"). Put it in the app header beside the account menu. The button is 32 px.

## States

- `data-warn` (default 80) and `data-down` (default 95) set `data-level`, which colours the arc.

## Keys

- A native button opening a native popover: Enter/Space opens it, Esc closes it. The label states the percent and the popover states the numbers.

## Events

None; the component fires no events of its own.

## Do and don't

- Don't: let the ring be the only signal.
