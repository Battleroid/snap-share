The command palette is a ⌘K search for pages, hosts and actions.

## Markup

- `dialog.pb-dialog.pb-cmd[id]` with `pb-cmd__search` (icon, input, `esc` key hint), `ul.pb-cmd__list` of `li.pb-cmd__group` headings and `li.pb-cmd__item` (`data-cmd`, `data-keywords`, icon, `pb-cmd__label`, `pb-cmd__hint`), a hidden `pb-cmd__empty` and `pb-cmd__foot`.
- Data-driven items: put `<script type="application/json">` inside the dialog, or `data-cmd-src="/url"` on it (fetched once, on first open, as JSON). Both take `[{label, items:[{label, href, action, keywords, hint, icon}]}]` or `{sections:[…]}`; each section becomes a `pb-cmd__group` and its items follow the markup ones. `dlg.pbAdd(data)` adds more at any time.
- CSP: `data-cmd-src` is a fetch, so the page needs `connect-src 'self'` (or the origin that serves it). Inline JSON needs nothing.
- Trigger: the header search button `button[data-pb-open="#id"]` with the hint `<span class="pb-kbd" data-kbd="mod+K">⌘K</span>`: the `kbd` hook writes ⌘K on Apple devices and Ctrl+K elsewhere (`pbKey('mod+K')` from script).

## States

- Text is escaped; only `http(s)` and relative `href`s are kept.
- The list shows an `aria-busy` look while a `data-cmd-src` fetch is pending.
- Empty: the `pb-cmd__empty` message shows when nothing matches.

## Keys

- `pbCommand(dlg)` opens on ⌘K / Ctrl+K and from any `button[data-pb-open="#id"]`; the palette resets and focuses its input on `pb:show`. ⌘K is ignored when another handler already called `preventDefault()` (an editor's own Ctrl+K), so it never steals the key.
- Typing filters (prefix and fuzzy, keywords count) and highlights the match. ↑/↓, Home/End (once an arrow key moved into the list), Enter and Esc work through `pbListKeys` with `aria-activedescendant`.

## Events

- Listens for `pb:show` on the dialog to reset and focus.
- Running an item closes the palette and fires a bubbling, cancelable `pb:command` with `detail` `{command, label, href, item}` (`command` is `data-cmd`, else the label). An item with `data-href` is followed unless a listener calls `preventDefault()`.

## Do and don't

- Do: order groups Jump to (pages, services) first, then Actions.
- Do: show the item's shortcut in `pb-cmd__hint`.
- Don't: use it for anything that is not reachable another way.
