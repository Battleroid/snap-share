Phone patterns: a bottom tab bar for top-level sections and an action sheet for an item's actions.

## Markup
- Tab bar: `nav.pb-tabbar[aria-label]` (`data-n="2"`…`"6"` items, 3 to 5 is best) of links with an icon and a short lowercase label; `aria-current="page"` on the current one, a `pb-count` for unread. A middle `button.pb-tabbar__fab` is the one main action. `pb-tabbar--fixed` pins it to the viewport bottom with the safe-area inset.
- Action sheet: `dialog.pb-sheet[id]` of `pb-sheet__group`s: a title (`pb-sheet__title`), `button.pb-sheet__item[data-value]` items (`--danger` for destructive), then a separate group with `pb-sheet__item--cancel`. Open it with `<button data-pb-open="#sheet-id">` or `sheet.pbOpen()`.
- `pb-phone` is the preview frame only.

## States
- `aria-current="page"` marks the current tab; `pb-count` shows unread.
- A sheet is modal. A backdrop tap closes it with no choice; choosing an item closes it.

## Keys
- The first item takes focus when the sheet opens (on `pb:show`). Esc closes it as for any dialog.
- Tab bar links and sheet items are native links and buttons.

## Events
- `pb:choose` bubbles from the sheet with the item's `data-value` as `detail` (an empty string when the item has none; the cancel item also fires it).
- `pb:show` fires on the sheet when it opens (`detail.opener`); it does not bubble.

## Do and don't
- Do: use the tab bar instead of the drawer when an app has four or five top-level places.
- Do: put the cancel item in its own group at the end.
- Don't: put more than five items in the tab bar.
