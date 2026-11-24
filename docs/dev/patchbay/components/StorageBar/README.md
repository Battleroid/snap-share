The storage bar shows how a pool's space splits across datasets.

## Markup

- `.pb-storage[data-storage]` with `pb-storage__head` (`pb-storage__name`, a badge, `pb-storage__total`), `.pb-storage__bar[role=img][aria-label]` of `span[data-key]` segments (`--w` width, `--c` color) and optional `i.pb-storage__mark` thresholds (`--at`, `data-status="down"` for the critical one), and `ul.pb-storage__legend` of `li[data-key]` (and `li[data-free]`).
- Set the segment and legend values with `data-vars="--w:44%;--c:var(--chart-1)"` (custom properties only; the `vars` hook applies them through the CSSOM). Never use a `style` attribute: the kit runs under `style-src 'self'`. For markup built as a string use `data-style` and call `pbStyled(container)`.
- Colors: chart and pop tokens, four segments at most; free space is the empty track.

## States

- `pb-storage__bar--joined` is one continuous bar: segments sit edge to edge with a hairline between them and only the two ends are rounded. Use it in dense places (dashboards, table cells, `pb-storage--compact`).
- Say the state in the badge (`86% full`) when past a threshold.

## Keys

- The bar is `role="img"` with an `aria-label`, so put the numbers in the label; the legend is plain text.
- `pbStorage(el)` highlights a segment and its legend row together on hover.

## Events

None; the component fires no events of its own.

## Do and don't

- Do: state the percentage in text, not only in the bar.
- Don't: use more than four segments.
