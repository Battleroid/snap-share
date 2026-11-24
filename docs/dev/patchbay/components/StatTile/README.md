StatTile is a metric tile with its history drawn as a background sparkline along the bottom edge.

## Markup

- `div.pb-tile` > `.pb-tile__label`, `.pb-tile__value` (unit in `small`), optional `.pb-tile__delta` (`data-tone="good|bad"`), and `svg.pb-tile__bg` with a `path.pb-tile__area` and a `path`, drawn with `preserveAspectRatio="none"` and `aria-hidden="true"`. The tile keeps a 60px strip at the bottom for the sparkline so it never runs under the text.
- Lay tiles out in `.pb-tiles`.

## States

- Grounds: default `surface`, `pb-tile--accent`, `--yellow`, `--mint`, `--red` (text and line in `on-accent` / `on-pop`), `--ink` (line in `pop-mint`). `pb-tile--hero` sets the value in `metric-xl`.

## Keys

Not interactive. The background sparkline is `aria-hidden`, so the value and delta must carry the meaning as text.

## Events

None; the component fires no events of its own.

## Do and don't

- Do: use at most two colored grounds per row.
- Don't: use more than one `pb-tile--hero` per page.
