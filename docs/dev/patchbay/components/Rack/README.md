The rack view lays out devices by rack unit with their status.

- Markup: `.pb-rack[data-rack][data-units]` with `pb-rack__title`, two empty `pb-rack__rail`s and `pb-rack__slots` of `button.pb-rack__dev[data-u][data-span][data-status=ok|warn|down|off]` (icon, `b` name, `small` address, `pb-rack__led`). `data-u` is the lowest unit it occupies. `data-kind="panel"` for patch panels, `div.pb-rack__dev[data-kind=blank]` for blanks.
- `pbRack(el)` places devices, numbers the rails and makes devices single-select toggles; it fires `pb:select` with the device (or null). Show details beside it.
- The LED color is backed by the device's status word in the detail panel. Addresses hide when the rack is narrow.
