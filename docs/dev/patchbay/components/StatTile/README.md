StatTile is a metric tile with its history drawn as a background sparkline along the bottom edge.

- Consumer provides: `div.pb-tile` > `.pb-tile__label`, `.pb-tile__value` (unit in `small`), optional `.pb-tile__delta` (`data-tone="good|bad"`), and `svg.pb-tile__bg` with a `path.pb-tile__area` and a `path`, drawn with `preserveAspectRatio="none"` and `aria-hidden="true"`. The tile keeps a 60px strip at the bottom for the sparkline so it never runs under the text.
- Grounds: default `surface`, `pb-tile--accent`, `--yellow`, `--mint`, `--red` (text and line in `on-accent` / `on-pop`), `--ink` (line in `pop-mint`). `pb-tile--hero` sets the value in `metric-xl`; one per page.
- Lay tiles out in `.pb-tiles`. Use at most two colored grounds per row.
