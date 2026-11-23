The storage bar shows how a pool's space splits across datasets.

- Markup: `.pb-storage[data-storage]` with `pb-storage__head` (`pb-storage__name`, a badge, `pb-storage__total`), `.pb-storage__bar[role=img][aria-label]` of `span[data-key]` segments (`--w` width, `--c` color) and optional `i.pb-storage__mark` thresholds (`--at`, `data-status="down"` for the critical one). Set them with `data-vars="--w:44%;--c:var(--chart-1)"` (no inline style; the `vars` hook applies it), and the legend swatches with `data-vars="--c:…"`, and `ul.pb-storage__legend` of `li[data-key]` (and `li[data-free]`).
- `pb-storage__bar--joined` is one continuous bar: segments sit edge to edge with a hairline between them and only the two ends are rounded. Use it in dense places (dashboards, table cells, `pb-storage--compact`).
- `pbStorage(el)` highlights a segment and its legend row together on hover.
- Colors: chart and pop tokens, four segments at most; free space is the empty track. Say the state in the badge (`86% full`) when past a threshold.
