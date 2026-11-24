Diagram lays out topology: positioned nodes, dashed zones and an SVG layer of edges.

## Markup

- Consumer provides: `.pb-diagram` (fixed height) holding an empty `<svg>`, `.pb-zone` boxes with a `.pb-zone__label`, and `.pb-node` elements (icon, `.pb-node__name`, `.pb-node__host`, optional `pb-dot`) with a `data-id`, placed with `left` / `top`.
- Place nodes and zones with a class of your own that sets `left` / `top`, or write `data-style="left:50%;top:6px"` and call `pbStyled(diagram)` once; the kit runs under `style-src 'self'`, so no inline `style` attributes.
- Roles: `data-role="edge"` (`pop-yellow`, the internet or WAN), `data-role="core"` (`accent`, the gateway).
- Edges: draw orthogonal paths between node centers into the svg after layout (and on resize and `document.fonts.ready`). `pb-edge`, `pb-edge--active` (accent), `pb-edge--down` (dashed); `pb-edge-label` for link speed.
- For direction (requests, data, backups), add arrowheads: see Flow.

## States

- `data-status="down"` outlines a node in `down`; a `pb-dot[data-status]` inside a node shows its state.
- Edge styles: plain, `pb-edge--active`, `pb-edge--down`.

## Keys

- The links are drawn in an SVG that is `aria-hidden`; `pbDiagramText(diagram, links, state)` adds a visually hidden list of each link ("gateway to pi-dns, 1G, down").
- A diagram that scrolls sideways becomes a named, focusable region. Node dots say their state (the `dot` hook).

## Events

- None; the component fires no events of its own.

## Do and don't

- Do: redraw the edges on resize and when fonts load.
- Do: call `pbDiagramText` so the links exist as text.
- Don't: use inline `style` attributes to position nodes.
