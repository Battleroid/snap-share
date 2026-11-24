Gauges show one value against its limits; donuts show parts of a whole.

## Markup

- Dial: `figure.pb-dial[data-gauge]` with `data-value`, `data-min`, `data-max`, `data-warn`, `data-down`, `data-unit` and a `figcaption` (name, `small` detail). `pbDial(el)` draws the arc and threshold ticks. `el.pbSet(v)` updates it.
- Donut: `figure.pb-donut[data-gauge-donut]` with an empty `pb-donut__hole` and `ul.pb-donut__legend` of `li[data-value]` (`data-vars="--c:var(--chart-1)"`, `i`, name, value, `span[data-pct]`). `data-total` and `data-total-label` set the center. `pbDonut(el)` draws it and links hover between ring and legend.

## States

- A dial colors `warn` / `down` past its limits (`data-status`) and adds a status line ("high · above 100GB").

## Keys

- The dial's name joins the caption's parts with commas and the value: "CPU, pve-01 · 32 threads, 34% of 100%".
- Not interactive: hover on a donut links ring and legend, but nothing takes focus.

## Events

- None; the component fires no events of its own.

## Do and don't

- Do: use a gauge for one value against its limits, a donut for parts of a whole.
- Don't: give a donut more than five parts; past that use horizontal bars.
