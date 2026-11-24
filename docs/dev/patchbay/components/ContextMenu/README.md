Context menus offer actions for the thing under the pointer.

- Markup: give the zone `data-contextmenu="menu-id"` and `tabindex="0"`; the menu is a normal `div.pb-menu[role=menu][id]`. `pbContext(zone)` opens it at the pointer on right-click, or under the focused row on Shift+F10 / the Menu key.
- The menu closes on Escape, Tab, an outside click, scrolling the page or choosing an item; focus returns to the zone. ↑/↓ cycle, Home/End jump to the ends, typing letters jumps to the next matching item (`pbListKeys`). One menu can serve several zones, and a second `pbInit` never binds the keys twice.
- Every action in a context menu must also be available another way (a `more` button or toolbar). Keep the order the same as that menu.
