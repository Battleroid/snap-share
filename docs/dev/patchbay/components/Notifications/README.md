Notifications are a bell with an unread count that opens an inbox of things that happened while the user was away.

- Consumer provides: a wrapper with `button.pb-btn--icon.pb-bell` (`pb-icon--bell`, `span.pb-bell__count`) and `div.pb-inbox[popover][role=dialog]` > `.pb-inbox__head` (`h3.pb-inbox__title`, a ghost "Mark all read" with `data-readall`) > `ul.pb-inbox__list` of `li.pb-notif` (`data-status`, `data-unread`, `tabindex="0"`; icon, `.pb-notif__title`, `.pb-notif__time`, optional `.pb-notif__body`) > hidden `p.pb-inbox__empty` > optional `.pb-inbox__foot` link.
- Script: `pbInbox(wrapper)` opens the panel under the bell, keeps the count and the bell's `aria-label` in sync, marks an item read on click or Enter, and moves with ↑↓. `pb-inbox--inline` renders the panel in the page (activity sidebar).
- Unread items get an `accent` dot, a tint and a bold title. Times are short and relative (`2m`, `1h`, `yesterday`), the body says the cause or result in one line.
