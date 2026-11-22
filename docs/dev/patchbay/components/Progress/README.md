Progress uses the native `<progress>` and `<meter>` elements, laid out with `.pb-gauge`.

- `progress.pb-progressbar` with `value`/`max` for tasks with a known end; without `value` it shows moving stripes (still under reduced motion).
- `meter.pb-meterbar` with `low`, `high`, `optimum` for measurements in a range: the browser picks `ok`, `warn` or `down`. For disk use: `low=70 high=90 optimum=10`.
- `.pb-gauge` > `.pb-gauge__label`, `.pb-gauge__value`, then the bar. Always print the value as text.
