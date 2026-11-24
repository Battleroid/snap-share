The cron builder edits a schedule and says it back in words with the next run times.

## Markup

- `.pb-cron[data-cron-builder]` with a `[data-cron-preset]` radiogroup (`hourly|daily|weekly|monthly|custom`), `pb-cron__fields` (five inputs: minute, hour, day, month, weekday), `p.pb-cron__human[aria-live]`, an expression input `[data-cron-expr]` and `ol.pb-cron__next`. `data-cron` sets the start value (default `0 3 * * *`); `data-next` sets how many runs are listed (default 5).
- `pbCron(el)` keeps the fields and the expression in sync, supports `*`, lists, ranges, steps, month and day names and `@daily`-style macros, and lists the next runs in local time.
- Helpers for app code: `pbCronParse(expr)`, `pbCronNext(c, from, n)`, `pbCronHuman(c)`.

## States

Invalid input sets `data-invalid` on the builder and turns the sentence into the error; the run list empties.

## Keys

Native inputs and a radiogroup: Tab and arrows as for those elements. The sentence is a live region, so changes are spoken.

## Events

A bubbling `pb:change` on the builder with the expression string as `detail`. It also fires once when the builder initialises.

## Do and don't

- Do: show the time zone next to the run list.
- Don't: treat the displayed run times as server times; they are local.
