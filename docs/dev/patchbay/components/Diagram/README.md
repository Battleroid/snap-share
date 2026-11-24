Diagram lays out topology: positioned nodes, dashed zones and an SVG layer of edges.

- Consumer provides: `.pb-diagram` (fixed height) holding an empty `<svg>`, `.pb-zone` boxes with a `.pb-zone__label`, and `.pb-node` elements (icon, `.pb-node__name`, `.pb-node__host`, optional `pb-dot`) placed with `left/top`.
- Roles: `data-role="edge"` (`pop-yellow`, the internet or WAN), `data-role="core"` (`accent`, the gateway). `data-status="down"` outlines a node in `down`.
- Edges: draw orthogonal paths between node centers into the svg after layout (and on resize and `document.fonts.ready`). `pb-edge`, `pb-edge--active` (accent), `pb-edge--down` (dashed); `pb-edge-label` for link speed.
- For direction (requests, data, backups), add arrowheads: see Flow.
- The links are drawn in an SVG that is `aria-hidden`; `pbDiagramText(diagram, links, state)` adds a visually hidden list of each link ("gateway to pi-dns, 1G, down"), and a diagram that scrolls sideways becomes a named, focusable region. Node dots say their state (the `dot` hook).
