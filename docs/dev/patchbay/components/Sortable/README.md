Sortable lists let people reorder items: boot order, dashboard tiles, priorities.

## Markup

- `ol.pb-sortable[data-sortable]` (or `pb-sortable--grid` for tiles) of `li.pb-sort__item[data-id]` with a `button.pb-sort__handle` (grip icon), optional `pb-sort__num`, an element marked `data-sort-label` and `pb-sort__meta`.
- `pb-sortable--tiles` is the bare grid variant: each `li.pb-sort__item` is only a wrapper (no padding, border or background) around your own tile, for example a `pb-tile-media`; the handle floats in the top-right corner and is the only part that blocks touch scrolling (`touch-action: none`), so the page still scrolls when you swipe across tiles.
- Announcements and labels come from `pbStrings` (`sortReorder`, `sortGrabbed`, `sortDropped`, `sortMoved`, `sortCanceled`; they take `{name}`, `{i}` and `{n}`).

## States

- While dragging, a placeholder marks the drop position.

## Keys

- `pbSortable(list)` supports dragging by the handle (mouse, pen, touch). Keyboard: focus the handle, Space to pick up, arrows to move, Space to drop, Esc to cancel. Each move is announced.
- `pb-sortable--tiles` implies grid keyboard moves (←/→/↑/↓ in two dimensions); it does not need `pb-sortable--grid` as well.

## Events

- `pb:reorder` (bubbling, from the list) with `detail` the ids in the new order.

## Do and don't

- Do: save the order right away or with the section's Save.
- Don't: start a drag from anywhere but the handle; that keeps text selectable.
