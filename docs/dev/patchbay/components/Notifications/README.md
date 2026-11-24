Notifications are a bell with an unread count that opens an inbox of things that happened while the user was away.

## Markup
- A wrapper with `data-inbox`, holding `button.pb-btn--icon.pb-bell` (`pb-icon--bell`, `span.pb-bell__count`) and `div.pb-inbox[popover][role=dialog]` > `.pb-inbox__head` (`h3.pb-inbox__title`, a ghost "Mark all read" with `data-readall`) > `ul.pb-inbox__list` of `li.pb-notif` (`data-status`, `data-unread`, `tabindex="0"`; icon, `.pb-notif__title`, `.pb-notif__time`, optional `.pb-notif__body`) > hidden `p.pb-inbox__empty` > optional `.pb-inbox__foot` link.
- Script: `pbInbox(wrapper, {unread, lazy, onOpen, onRead, onReadAll})` (the `inbox` hook runs it for `[data-inbox]`). It opens the panel under the bell and keeps the count and the bell's `aria-label` in sync. `unread` is a server count shown on the bell until the items say otherwise. Calling `pbInbox` again on a bound inbox only updates the options.
- `lazy` is a function `(panel) => Promise` that fills the panel on first open, or `true` to fetch the HTML at `data-inbox-src` (on the inbox or its panel) into `.pb-inbox__list`. That fetch is same-origin and needs `connect-src 'self'` in the page's CSP (or the origin you fetch from, when you use your own function).
- `pb-inbox--inline` renders the panel in the page (activity sidebar).

## States
- Unread items get an `accent` dot, a tint and a bold title. The count hides at zero.
- While a lazy panel loads it has `aria-busy="true"`; a failed load is retried on the next open.
- With no items the `pb-inbox__empty` line shows.
- Times are short and relative (`2m`, `1h`, `yesterday`), the body says the cause or result in one line.

## Keys
- The bell is a toggle: click opens, click again closes, with or without `popovertarget` on it (with it, the browser toggles and the kit only places and focuses the panel).
- Opening focuses the first item. ↑/↓ move between items, Enter opens the focused one (and marks it read). Escape and outside click close the panel natively.
- The bell's `aria-label` is "Notifications" plus the unread count.

## Events
- `pb:read` (`detail: {item, wasUnread}`) fires on the wrapper for every item opened, read or not; `onRead(item, wasUnread)` is the same as an option.
- `pb:readall` (`detail: {items}`) fires when Mark all read is clicked and is cancelable: call `preventDefault()` on it, or pass `onReadAll(items)`, and the button's own default (for example a form POST) is prevented while the items are still marked read.

## Do and don't
- Do: use `lazy` for an inbox you fetch, and allow its source in `connect-src`.
- Do: say the cause or result in one line.
- Don't: show a count only in color; the number and `aria-label` carry it.
