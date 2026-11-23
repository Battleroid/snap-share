Context menus offer actions for the thing under the pointer.

- Markup: give the zone `data-contextmenu="menu-id"` and `tabindex="0"`; the menu is a normal `div.pb-menu[role=menu][id]`. `pbContext(zone)` opens it at the pointer on right-click, or under the focused row on Shift+F10 / the Menu key.
- The menu closes on Escape, an outside click, scrolling or choosing an item; focus returns to the zone. ↑/↓ move between items.
- Every action in a context menu must also be available another way (a `more` button or toolbar). Keep the order the same as that menu.
