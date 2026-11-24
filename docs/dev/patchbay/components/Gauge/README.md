Gauges show one value against its limits; donuts show parts of a whole.

- Dial: `figure.pb-dial[data-gauge]` with `data-value`, `data-min`, `data-max`, `data-warn`, `data-down`, `data-unit` and a `figcaption` (name, `small` detail). `pbDial(el)` draws the arc and threshold ticks, colors it `warn`/`down` past the limits and adds a status line ("high · above 100GB"). `el.pbSet(v)` updates it.
- Donut: `figure.pb-donut[data-gauge-donut]` with an empty `pb-donut__hole` and `ul.pb-donut__legend` of `li[data-value]` (`--c`, `i`, name, value, `span[data-pct]`). `data-total` and `data-total-label` set the center. `pbDonut(el)` draws it and links hover between ring and legend.
- Donuts take five parts at most; past that use horizontal bars.
- The dial's name joins the caption's parts with commas and the value: "CPU, pve-01 · 32 threads, 34% of 100%".
