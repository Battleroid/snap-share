The cron builder edits a schedule and says it back in words with the next run times.

- Markup: `.pb-cron[data-cron-builder]` with a `[data-cron-preset]` radiogroup (`hourly|daily|weekly|monthly|custom`), `pb-cron__fields` (five inputs: minute, hour, day, month, weekday), `p.pb-cron__human[aria-live]`, an expression input `[data-cron-expr]` and `ol.pb-cron__next`. `data-cron` sets the start value.
- `pbCron(el)` keeps the fields and the expression in sync, supports `*`, lists, ranges, steps, month and day names and `@daily`-style macros, and lists the next `data-next` (default 5) runs in local time. Invalid input turns the sentence into the error. It fires `pb:change` with the expression.
- Helpers for app code: `pbCronParse(expr)`, `pbCronNext(c, from, n)`, `pbCronHuman(c)`.
- Show the time zone next to the run list.
