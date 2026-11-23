Breadcrumbs show where a page sits and link back up the trail.

- Markup: `nav.pb-crumbs[aria-label="Breadcrumb"] > ol > li`, links for ancestors, `span[aria-current="page"]` for the current page. Separators are drawn by CSS.
- `pb-crumbs--path` is the mono form for file paths and datasets, with `/` separators.
- Each item caps at 220px with an ellipsis. For deep trails, keep the first and the last two and put the rest behind a `…` button that opens a `pb-menu` popover.
- Breadcrumbs sit at the top of `pb-pagehead`, above the title. Skip them on top-level pages.
