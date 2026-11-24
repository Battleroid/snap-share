Breadcrumbs show where a page sits and link back up the trail.

## Markup

- `nav.pb-crumbs[aria-label="Breadcrumb"] > ol > li`, links for ancestors, `span[aria-current="page"]` for the current page. Separators are drawn by CSS.
- Breadcrumbs sit at the top of `pb-pagehead`, above the title.
- For deep trails, keep the first and the last two and put the rest behind a `…` button that opens a `pb-menu` popover.

## States

- `pb-crumbs--path` is the mono form for file paths and datasets, with `/` separators.
- Each item caps at 220px with an ellipsis.

## Keys

Links and the `…` button are native controls: Tab and Enter. The current page carries `aria-current="page"`.

## Events

None; the component fires no events of its own.

## Do and don't

- Do: skip them on top-level pages.
- Don't: link the current page.
