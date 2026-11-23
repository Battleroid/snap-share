The dashboard layout arranges tiles, charts and lists on a 12-column grid.

- Markup: `.pb-dash` with children setting `data-span="1"`…`"12"` (out of 12; default 4). The attribute needs no inline style; `--span` in a stylesheet still works. `pb-tile` for headline numbers, `section.pb-dash__panel` with `pb-dash__head` (`h3`, link) for everything else.
- The grid fills gaps densely and responds to its own width: under 900px items take at least half, under 560px everything is full width. It works the same inside a pane or the full page.
- Order: headline tiles (four at most), then the main chart, then status (gauges, storage), then activity and the service list.
- Put the time range switch in `pb-pagehead__actions` and say when data was updated.
