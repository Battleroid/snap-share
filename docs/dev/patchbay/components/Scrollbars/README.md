Scrollbars are thin and take their color from the theme so they sit quietly on every ground.

## Markup
- Automatic inside `.pb`: `scrollbar-width: thin` and an `ink` thumb at 28% on a clear track. Night grounds (terminal, player, lightbox, ink panels, bulk bar) use a light thumb.
- `pb-scroll-none` hides the bar for strips that scroll by other means (tabs, chip rows).
- The `scroll` hook does the keyboard work for `.pb-tablescroll`, prose `pre` and tables, the sub-nav and settings nav rows and any `[data-scroll]`; components that scroll (heatmap, diff, resizable panes, tabs, log lines) call `pbScroller` themselves. For your own box use `data-scroll`, `pbScroller(el, name)` or `pbScrollRegion(el, name)`; `data-more-start` / `data-more-end` drive the `.pb-scrollfade` edge fade.

## States
- Browsers without `scrollbar-color` get the same look through `::-webkit-scrollbar`: rounded thumbs in soft, square in hard (with a 55% thumb in the hard cut, in that fallback only).
- While a box overflows and holds nothing focusable it gets `tabindex=0`, `role=region` and a name; when it stops overflowing the kit takes them away again.

## Keys
- Any box that scrolls needs to be reachable by keyboard: `tabindex="0"`, a `role="region"` and an `aria-label`. The kit does this for terminal output and the file preview details, and the hook does it for the boxes above.
- Strips that use `pb-scroll-none` must stay reachable by keyboard.

## Events
None; the component fires no events of its own (scroll boxes fire native `scroll`).

## Do and don't
- Do: give a scrolling region a name (`aria-label`).
- Do: keep `pb-scroll-none` strips reachable by keyboard.
- Don't: hide a scrollbar on a box that has no other way to scroll.
