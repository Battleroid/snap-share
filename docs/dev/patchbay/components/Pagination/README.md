Pagination moves through long result sets one page at a time.

## Markup
- `nav.pb-pager[data-pager]` with `data-total` (pages), `data-page`, `data-size`, `data-items` (total results), an empty `ul.pb-pager__pages`, `pb-pager__info` and optionally a `pb-pager__size` select. `pbPager(el)` renders the numbers with gaps (1 … 5 6 7 … 16) and marks the current page with `aria-current`.
- Compact form: Newer / Older buttons around "page 1 of 16". Use it for feeds and logs where page numbers mean little. These are plain buttons you wire yourself.
- The info text ("101–120 of 312") is filled in when `data-items` is set. The size select is markup only: listen to its `change` and rebuild the pager.

## States
- The current page has `aria-current="page"`; Previous is disabled on page 1 and Next on the last page.
- Gaps (`…`) are `aria-hidden`.
- Printing hides the pager.

## Keys
- Native buttons: Tab and Enter/Space. After a click, focus moves to the new current page button, so keyboard users stay in the list.
- Each page button is named "Page N"; Previous and Next have their own names.

## Events
- `pb:page` bubbles from the nav with the new page number as `detail`. It also fires once when the pager first renders.

## Do and don't
- Do: show "101–120 of 312" so people know where they are.
- Do: keep the size choice to 20, 50, 100.
- Don't: make people page when filters and search would do; use Load more only for feeds.
