Context menus offer actions for the thing under the pointer.

- Markup: give the zone `data-contextmenu="menu-id"` and `tabindex="0"`; the menu is a normal `div.pb-menu[role=menu][id]`. `pbContext(zone)` opens it at the pointer on right-click, or under the focused row on Shift+F10 / the Menu key.
- The menu closes on Escape, Tab, an outside click, scrolling the page or choosing an item; focus returns to the zone. ↑/↓ cycle, Home/End jump to the ends, typing letters jumps to the next matching item (`pbListKeys`). One menu can serve several zones, and a second `pbInit` never binds the keys twice.
- Every action in a context menu must also be available another way (a `more` button or toolbar). Keep the order the same as that menu.
- Rows: the zone's rows (`data-rows="selector"`, default its child `li`) are one tab stop with ↑/↓ and Home/End; the zone itself is no longer one. Right-click or Shift+F10 opens the menu for that row and marks it `data-ctx-row`; choosing an item fires a bubbling `pb:command` on the zone with `detail` `{command, label, item, row}` (`command` is the item's `data-cmd`, else its label; a distinct name, since the file browser, tree and rack already fire `pb:select`); Esc, Tab or a choice closes it and focus goes back to the row.
- A context menu is hidden by nature: pair it with a visible "…" (More) button on each row that opens the same actions.
