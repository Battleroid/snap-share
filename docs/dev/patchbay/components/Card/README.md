A card is the base surface: `surface`, a border, `radius-lg` (soft) and the edge variant's shadow.

- Consumer provides: `<div class="pb-card">` with optional `.pb-card__head` (a `.pb-card__title` and an eyebrow or action) and any body.
- `pb-card__band` is a pop header strip for one message that needs attention ("Certificate expiring"); default `pop-yellow`, or `--red`, `--mint`, `--accent`. One band per card, text in `label` style with an icon.
- `pb-card--outline` is a flat panel with a `line-strong` (soft) or `ink` (hard) outline that turns `accent` on hover, keyboard focus inside it, or `aria-current="true"`. Soft thickens the outline; hard adds a 4px `accent` offset shadow. Use it for pickable panels: service tiles, plan options, file cards. Make the whole panel an `<a class="pb-card pb-card--outline">` or `<button>` when it is one action.
- Hover color: `pb-card--ok`, `--warn`, `--down`, `--ink`, or set `--pb-outline-hover` to another text-safe color token.
- `pb-card--flat` drops the shadow for cards nested in cards. Do not nest more than one level.
