The bulk action bar appears when rows are selected and acts on all of them.

## Markup

- Wrap the table or list in `[data-bulk]`; row checkboxes are `.pb-check[data-row]`, the header box `[data-all]`.
- Add `.pb-bulkbar[role=region][aria-label]` with `[data-bulk-count]`, `pb-bulkbar__sep`, buttons with `data-bulk-action="name"` and a `[data-bulk-clear]` button. Put the bar inside the table wrap, after the table: it docks at the bottom of the container and sticks to the bottom of the screen while a long table scrolls.
- The `bulk` hook binds `[data-bulk]` only when it holds a `.pb-bulkbar`, so an app's own `button[data-bulk]` (or any other use of the attribute) is left alone.
- Grids and tiles: rows are the closest `[data-bulk-item]`, `tr`, `.pb-item` or `li`, so a tile grid works the same: put a `.pb-check[data-row]` in each tile and a `[data-all]` box anywhere in the scope. `.pb-bulkgrid` is a ready grid.
- Select all matching: set `data-bulk-total="248"` (everything the filter matches, across pages) on the scope, and add `[data-bulk-matching]`, `[data-bulk-matching-off]` and a hidden `[data-bulk-matching-field]` input.
- Words come from `pbStrings`: `bulkSelected`, `bulkAllMatching`, `bulkSelectMatching`, `bulkOnlyPage` (each takes `{n}`).

## States

- `pbBulk(scope)` shows the bar while anything is selected, keeps the header box in sync (indeterminate for a partial selection), and marks selected rows (`data-selected`, plus `aria-selected` on table rows).
- Once every box on the page is checked and `data-bulk-total` exceeds the page, the bar offers "Select all 248 matching"; choosing it shows "All 248 matching selected", enables the hidden field so a form submit tells the server "all matching", and shows "Only this page" to go back. Unchecking any box drops it.

## Keys

- Space toggles a box. Shift+click, or Shift+Space on a focused box, sets every box between it and the last one clicked to the clicked box's new state.
- A tile with `tabindex="0"` toggles its box on Space (Shift+Space the range).
- Esc or the clear button empties the selection; the clear button then focuses the first box.

## Events

Bubbling `CustomEvent`s on the scope:
- `pb:select` whenever the selection changes, `detail` `{items, all, count}`.
- `pb:bulk` when a bar action is pressed, `detail` `{action, items, all, count, total}`. With `all: true` and `count` equal to the total, act on the filter, not on `items`.

## Do and don't

- Do: put destructive actions last with `pb-btn--danger`, and confirm them with a Dialog.
- Do: act on the filter when `all` is true.
- Don't: reuse `data-bulk` on an element that has no `.pb-bulkbar`.
