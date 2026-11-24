Icons are a 76-glyph, 24px, 1.75px-stroke set in two cuts: round caps and circular frames (soft), square caps and square frames (hard).

## Markup

- Use: `<span class="pb-icon pb-icon--NAME"></span>`. It paints in `currentColor` and swaps cut with the nearest `data-edge`. Sizes: inherits 1.25em, or `pb-icon--16/20/24`.
- Names: `activity, arrow-down, arrow-up, bell, calendar, captions, chart, check, chevron-down, chevron-right, clock, close, cloud, copy, cpu, database, diff, download, eye, eye-off, file, filter, folder, fullscreen, fullscreen-exit, grid, grip, home, image, inbox, key, keyboard, link, link-external, link-internal, list, lock, menu, minus, monitor, moon, more, network, pause, pip, play, plus, power, printer, qr, refresh, search, server, settings, shield, skip-back, skip-forward, sort, status-down, status-info, status-maint, status-ok, status-paused, status-unknown, status-warn, sun, terminal, trash, unlock, upload, user, volume, volume-off, wrap, zoom-in, zoom-out`.
- SVG files of both cuts are in the Icons Soft and Icons Hard asset groups.
- CSP: icons are CSS masks that use `data:` image URIs, so the policy needs `img-src data:`. They need nothing else: no font, no sprite and no request.

## States

- Two cuts of a 76-glyph, 24px, 1.75px-stroke set: round caps and circular frames (soft), square caps and square frames (hard). The cut follows the nearest `data-edge`.
- Color is `currentColor`; a status icon takes its status color from its parent.

## Keys

- Not focusable. Icon-only buttons need an `aria-label`.

## Events

- None; the component fires no events of its own.

## Do and don't

- Do: give icon-only buttons an `aria-label`.
- Do: put a word next to every status icon.
- Don't: use an icon as the only signal of a state.
