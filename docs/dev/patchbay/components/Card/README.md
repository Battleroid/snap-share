A card is the base surface: `surface`, a border, `radius-lg` (soft) and the edge variant's shadow.

## Markup

- `<div class="pb-card">` with optional `.pb-card__head` (a `.pb-card__title` and an eyebrow or action) and any body. The base surface is `surface`, a border, `radius-lg` (soft) and the edge variant's shadow.
- `pb-card__band` is a pop header strip for one message that needs attention ("Certificate expiring"), text in `label` style with an icon.
- Make the whole panel an `<a class="pb-card pb-card--outline">` or `<button>` when it is one action.

## States

- Band: default `pop-yellow`, or `--red`, `--mint`, `--accent`. One band per card.
- `pb-card--outline` is a flat panel with a `line-strong` (soft) or `ink` (hard) outline that turns `accent` on hover, keyboard focus inside it, or `aria-current="true"`. Soft thickens the outline; hard adds a 4px `accent` offset shadow.
- Hover color: `pb-card--ok`, `--warn`, `--down`, `--ink`, or set `--pb-outline-hover` to another text-safe color token.
- `pb-card--flat` drops the shadow for cards nested in cards.

## Keys

A card is not focusable unless it is itself an `a` or `button`.

## Events

None; the component fires no events of its own.

## Do and don't

- Do: use `pb-card--outline` for pickable panels: service tiles, plan options, file cards.
- Don't: nest cards more than one level.
- Don't: put more than one band on a card.
