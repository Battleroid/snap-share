The rack view lays out devices by rack unit with their status.

## Markup
- `.pb-rack[data-rack][data-units]` with `pb-rack__title`, two empty `pb-rack__rail`s and `pb-rack__slots` of `button.pb-rack__dev[data-u][data-span][data-status=ok|warn|down|off]` (icon, `b` name, `small` address, `pb-rack__led`). `data-u` is the lowest unit it occupies. `data-kind="panel"` for patch panels, `div.pb-rack__dev[data-kind=blank]` for blanks.
- `pbRack(el)` (the `rack` hook) places devices, numbers the rails and makes devices single-select toggles. Show details beside it.

## States
- `data-status` is `ok`, `warn`, `down` or `off`. The LED color is backed by the device's status word, which joins the device's accessible name.
- A selected device has `aria-pressed="true"`; only one is selected at a time, and clicking it again clears the selection.
- Addresses hide when the rack is narrow.

## Keys
- Devices are native buttons: Tab and Space/Enter toggle one. Each device's name joins its parts and state: "pve-02, 10.0.10.12, ok" (unless you set `aria-label`).
- The unit numbers on the rails are `aria-hidden`.

## Events
- `pb:select` bubbles from the rack; its `detail` is the device element, or `null` when the selection is cleared.

## Do and don't
- Do: show the selected device's details beside the rack, with the status as a word.
- Don't: rely on the LED color alone.
