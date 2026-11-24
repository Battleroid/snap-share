Flow is a directed diagram: Diagram's nodes joined by arrows that show which way data or requests move.

- Consumer provides: the Diagram markup (`.pb-diagram`, `svg`, positioned `.pb-node`s) and, in the svg, `marker`s whose path has class `pb-arrowhead` (`--accent`, `--down`); give marker ids a per-diagram prefix. Reference them with `marker-end` (and `marker-start` for two-way links).
- Arrowheads are open chevrons in soft and filled triangles in hard.
- Edges: `pb-edge--active` (accent) for the main request path, `pb-edge--async` (dashed) for scheduled or background work, `pb-edge--down` for a failing link. Label each edge with `pb-edge-label` (protocol or action).
- Add a `pb-legend` under the diagram when more than one edge style is used. Wrap it in a horizontal scroller on narrow screens.
- The arrows are an `aria-hidden` SVG; `pbDiagramText` lists each link with its state (active, async, down) for screen readers, as in Diagram.
