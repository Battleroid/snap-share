Flow is a directed diagram: Diagram's nodes joined by arrows that show which way data or requests move.

## Markup

- Consumer provides: the Diagram markup (`.pb-diagram`, `svg`, positioned `.pb-node`s; position them with a class or `data-style` plus `pbStyled`, not inline `style`) and, in the svg, `marker`s whose path has class `pb-arrowhead` (`--accent`, `--down`); give marker ids a per-diagram prefix. Reference them with `marker-end` (and `marker-start` for two-way links).
- Edges: `pb-edge--active` (accent) for the main request path, `pb-edge--async` (dashed) for scheduled or background work, `pb-edge--down` for a failing link. Label each edge with `pb-edge-label` (protocol or action).
- Add a `pb-legend` under the diagram when more than one edge style is used.

## States

- Arrowheads are open chevrons in the soft cut and filled triangles in the hard cut.
- Edge states: active, async, down.
- Wrap the diagram in a horizontal scroller on narrow screens.

## Keys

- The arrows are an `aria-hidden` SVG; `pbDiagramText` lists each link with its state (active, async, down) for screen readers, as in Diagram.

## Events

- None; the component fires no events of its own.

## Do and don't

- Do: label each edge with its protocol or action.
- Do: add a legend when more than one edge style is used.
- Don't: reuse marker ids across diagrams.
