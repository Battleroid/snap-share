Empty and result states explain an empty list, a search with no matches, or a page that failed.

## Markup

- Empty: `.pb-empty` with `pb-empty__icon`, `h3.pb-empty__title`, `pb-empty__body` and `pb-empty__actions`. `pb-empty--compact` for no-results inside a panel.
- Result pages: `.pb-result[data-status]` with `pb-result__code` (404, 500), `pb-result__title`, `pb-result__body`, `pb-result__actions` and `pb-result__detail` (a reference id people can quote).
- Compact result: `.pb-result--compact` for a Result inside a card or auth card: centred, 21 px title, no page padding. Use `pb-result__icon` with a `pb-icon pb-result__glyph` instead of the big code.

## States

- `data-status` on a result colours the code (or the glyph in the compact result).
- Variants: empty, empty compact, result page, result compact.

## Keys

- Native: actions are links and buttons, in reading order.

## Events

- None; the component fires no events of its own.

## Do and don't

- Do: say what goes here and give the one action that fills it.
- Do: write the title as what happened ("That share is gone"), the body as what to do next. No apologies, no jokes.
- Do: offer Try again for 500s; link home and to the status page for 404s.
