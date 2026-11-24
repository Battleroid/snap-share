States for media whose preview is still being made, and a teaser that plays on hover or focus.

## Markup
- Processing: `.pb-proc` is a skeleton and a word (`role="status"`). `--tile` fills a media frame, `--note` is a line in a detail view; as a list lead, use it in place of `.pb-item__lead`. `pbProcessing({variant: 'lead'|'tile'|'note', state, label, attrs})` builds it; `attrs` adds attributes such as `hx-get` and `hx-trigger="every 5s"` for polling.
- Teaser: `data-teaser="/preview.mp4"` on an element that holds the poster `img` (the img's parent is the frame). The video URL has to be allowed by the page's `media-src` (or `default-src`).

## States
- Processing shows the skeleton and a word; `data-state="failed"` shows the failure with an icon and a word.
- Teaser: a mouse resting 250 ms, or keyboard focus (focus-visible) anywhere inside, plays a muted looping video over the poster, with a thin progress line. One plays at a time; touch and pen are ignored; Save-Data and a teaser that fails to load leave the poster.
- Under reduced motion the teaser still plays (it only starts on a hover or focus) but without the fade.

## Keys
- Teaser: keyboard focus inside the host starts it; blur or Esc stops it. The processing stand-in is not interactive.
- The video is `aria-hidden`: anything it shows must also be in the item's text.

## Events
None; the component fires no events of its own. A polling `attrs` (for example `hx-get`) is handled by whatever library you bring.

## Do and don't
- Do: say the state in a word ("processing", "failed"), never only a skeleton.
- Do: repeat anything the teaser shows in the item's text.
- Don't: rely on the teaser on touch devices; it does not play there.
