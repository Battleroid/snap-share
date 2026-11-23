Progress uses the native `<progress>` and `<meter>` elements, laid out with `.pb-gauge`.

- `progress.pb-progressbar` with `value`/`max` for tasks with a known end; without `value` it shows moving stripes (slower under reduced motion). Update `value` as work goes; the fill eases between steps.
- `meter.pb-meterbar` with `low`, `high`, `optimum` for measurements in a range: the browser picks `ok`, `warn` or `down`. For disk use: `low=70 high=90 optimum=10`.
- `.pb-gauge` > `.pb-gauge__label`, `.pb-gauge__value`, then the bar. Always print the value as text, and say "done" at the end.
- The last example runs live: a copy filling to done, and a button going busy (spinning icon, "Syncing") then confirming ("Synced").
