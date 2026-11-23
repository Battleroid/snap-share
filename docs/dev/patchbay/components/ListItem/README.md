List rows show a set of things with a lead, text, meta and actions on one line.

- Markup: `ul.pb-list > li > .pb-item` (`a` when the whole row opens something, `div` when it holds buttons). Parts: `pb-item__lead` (icon, initials or avatar), a span with `pb-item__title` and `pb-item__desc`, then `pb-item__end` with `pb-item__meta`, badges or buttons.
- `aria-current="true"` or `aria-selected="true"` marks the open or selected row. `pb-list--compact` tightens rows; `pb-list--plain` drops the outer border for use inside panes.
- A row is either a link or holds buttons, never both. Put secondary actions in a `more` menu.
- Use a Data table instead when people compare values across rows or sort by columns.
- Thumbnails: `span.pb-item__lead.pb-item__lead--thumb` holding an `img` (cover-cropped to the 36px square; add `pb-item__lead--wide` for 16:10 media).
- Media tiles: `ul.pb-mediagrid` (not `pb-tiles`, which is the stat-tile grid) (`data-vars="--tile-min:200px"` to change the column width) of `li > a.pb-tile-media` with `img.pb-tile-media__img` (`--tile-ratio`, default 4 / 3), `span.pb-tile-media__name` and `span.pb-tile-media__meta`. Use it for galleries, recent files and dashboard thumbnails.
