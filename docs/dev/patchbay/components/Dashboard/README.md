The dashboard layout arranges tiles, charts and lists on a 12-column grid.

## Markup

- `.pb-dash` with children setting `data-span="1"`…`"12"` (out of 12; default 4). The attribute needs no inline style; a `--span` custom property set in a stylesheet or through `data-vars="--span:5"` also works.
- `pb-tile` for headline numbers, `section.pb-dash__panel` with `pb-dash__head` (`h3`, link) for everything else.
- Per-item values (a bar width, a threshold position) go through `data-vars="--w:28.4%"` or a class, never a `style` attribute.
- Put the time range switch in `pb-pagehead__actions` and say when data was updated.

## States

- The grid fills gaps densely and responds to its own width (container query): under 900px items take at least half, under 560px everything is full width. It works the same inside a pane or the full page.

## Keys

None of its own; the tiles and panels carry their own controls and focus order. Use real headings (`h3`) in panel heads.

## Events

None; the component fires no events of its own.

## Do and don't

- Do: order headline tiles (four at most), then the main chart, then status (gauges, storage), then activity and the service list.
- Don't: use inline `style` for spans or widths.
