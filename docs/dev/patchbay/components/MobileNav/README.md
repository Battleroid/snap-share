Phone patterns: a bottom tab bar for top-level sections and an action sheet for an item's actions.

- Tab bar: `nav.pb-tabbar[aria-label]` (`data-n="2"`…`"6"` items, 3 to 5 is best) of links with an icon and a short lowercase label; `aria-current="page"` on the current one, a `pb-count` for unread. A middle `button.pb-tabbar__fab` is the one main action. `pb-tabbar--fixed` pins it to the viewport bottom with the safe-area inset. Use it instead of the drawer when an app has four or five top-level places.
- Action sheet: `dialog.pb-sheet[id]` of `pb-sheet__group`s: a title (`pb-sheet__title`), `button.pb-sheet__item[data-value]` items (`--danger` for destructive), then a separate group with `pb-sheet__item--cancel`. Open it with `[data-sheet-open=id]` or `sheet.pbOpen()`; `pbSheet(d)` closes on a choice or a backdrop tap and fires `choose`.
- `pb-phone` is the preview frame only.
