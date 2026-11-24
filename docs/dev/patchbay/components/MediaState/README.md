States for media whose preview is still being made, and a teaser that plays on hover or focus.

- Processing: `.pb-proc` is a skeleton and a word (`role="status"`). `--tile` fills a media frame, `--note` is a line in a detail view; as a list lead, use it in place of `.pb-item__lead`. `data-state="failed"` shows the failure with an icon and a word. `pbProcessing({variant: 'lead'|'tile'|'note', state, label, attrs})` builds it; `attrs` adds attributes such as `hx-get` and `hx-trigger="every 5s"` for polling.
- Teaser: `data-teaser="/preview.mp4"` on an element that holds the poster `img` (the img's parent is the frame). A mouse resting 250 ms, or keyboard focus (focus-visible) anywhere inside, plays a muted looping video over the poster, with a thin progress line. Leaving, blur, Esc or a hidden tab stops it. One plays at a time; touch and pen are ignored; Save-Data and a teaser that fails to load leave the poster.
- Under reduced motion the teaser still plays (it only starts on a hover or focus) but without the fade.
- The video is `aria-hidden`: anything it shows must also be in the item's text.
