The bulk action bar appears when rows are selected and acts on all of them.

- Markup: wrap the table or list in `[data-bulk]`; row checkboxes are `.pb-check[data-row]`, the header box `[data-all]`. Add `.pb-bulkbar[role=region][aria-label]` with `[data-bulk-count]`, `pb-bulkbar__sep`, buttons with `data-bulk-action="name"` and a `[data-bulk-clear]` button. Put the bar inside the table wrap, after the table: it docks at the bottom of the container and sticks to the bottom of the screen while a long table scrolls.
- `pbBulk(scope)` shows the bar while anything is selected, keeps the header box and `aria-selected` in sync, clears on Esc or the X, and fires `bulk` with `{action, items}`.
- The bar sticks to the bottom of the scroll area on the `night` ground. Put destructive actions last with `pb-btn--danger`, and confirm them with a Dialog.
- The `bulk` hook binds `[data-bulk]` only when it holds a `.pb-bulkbar`, so an app's own `button[data-bulk]` (or any other use of the attribute) is left alone.
