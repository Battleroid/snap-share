Context menus offer actions for the thing under the pointer.

## Markup

- Give the zone `data-contextmenu="menu-id"` and `tabindex="0"`; the menu is a normal `div.pb-menu[role=menu][id]`. `pbContext(zone)` binds it. One menu can serve several zones, and a second `pbInit` never binds the keys twice.
- The zone's rows (`data-rows="selector"`, default its child `li`) are one tab stop; the zone itself is no longer one. The opened row is marked `data-ctx-row`.
- Pair every context menu with a visible "…" (More) button on each row that opens the same actions, in the same order.

## States

The menu is hidden until opened; it closes on Escape, Tab, an outside click, scrolling the page or choosing an item.

## Keys

- Right-click opens the menu at the pointer; Shift+F10 or the Menu key opens it under the focused row.
- Rows: ↑/↓ and Home/End move between them. In the menu: ↑/↓ cycle, Home/End jump to the ends, typing letters jumps to the next matching item (`pbListKeys`).
- Esc, Tab or a choice closes it and focus goes back to the row.

## Events

Choosing an item fires a bubbling `pb:command` on the zone with `detail` `{command, label, item, row}` (`command` is the item's `data-cmd`, else its label; a distinct name, since the file browser, tree and rack already fire `pb:select`).

## Do and don't

- Do: make every action available another way (a `More` button or toolbar).
- Don't: make a context menu the only route to an action.
