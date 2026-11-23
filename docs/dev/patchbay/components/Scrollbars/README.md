Scrollbars are thin and take their color from the theme so they sit quietly on every ground.

- Automatic inside `.pb`: `scrollbar-width: thin` and an `ink` thumb at 28% on a clear track (55% in the hard cut). Night grounds (terminal, player, lightbox, ink panels, bulk bar) use a light thumb.
- Browsers without `scrollbar-color` get the same look through `::-webkit-scrollbar`: rounded thumbs in soft, square in hard.
- `pb-scroll-none` hides the bar for strips that scroll by other means (tabs, chip rows); keep them reachable by keyboard.
- Give scrolling regions `tabindex="0"` and an `aria-label` so keyboard users can scroll them.
- Any box that scrolls needs to be reachable by keyboard: give it `tabindex="0"`, a `role="region"` and an `aria-label`. The kit does this for terminal output and the file preview details; `pbScrollRegion(el, name)` does it for your own.
