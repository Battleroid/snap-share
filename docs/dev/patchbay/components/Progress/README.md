Progress uses the native `<progress>` and `<meter>` elements, laid out with `.pb-progressrow`.

## Markup
- `progress.pb-progressbar` with `value`/`max` for tasks with a known end. Update `value` as work goes; the fill eases between steps.
- `meter.pb-meterbar` with `low`, `high`, `optimum` for measurements in a range. For disk use: `low=70 high=90 optimum=10`.
- `.pb-progressrow` > `.pb-progressrow__label`, `.pb-progressrow__value`, then the bar. Always print the value as text, and say "done" at the end.
- Name every bar: give the label an `id` and the `progress` or `meter` `aria-labelledby` pointing at it (the value text beside it is visual).

## States
- Without `value`, a `progress` shows moving stripes (slower under reduced motion).
- A `meter` is `ok`, `warn` or `down` as the browser compares its value with `low`/`high`/`optimum`.
- The last example runs live: a copy filling to done, and a button going busy (spinning icon, "Syncing") then confirming ("Synced").

## Keys
- Not interactive. Screen readers read the native role, value and the `aria-labelledby` name.

## Events
None; the component fires no events of its own.

## Do and don't
- Do: print the value as text beside the bar.
- Do: use `progress` for a known end and `meter` for a measurement in a range.
- Don't: leave a bar without a name.
