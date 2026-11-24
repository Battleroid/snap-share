Pagination moves through long result sets one page at a time.

- Markup: `nav.pb-pager[data-pager]` with `data-total` (pages), `data-page`, `data-size`, `data-items` (total results), an empty `ul.pb-pager__pages`, `pb-pager__info` and optionally a `pb-pager__size` select. `pbPager(el)` renders the numbers with gaps (1 … 5 6 7 … 16), marks the current page with `aria-current` and fires a `pb:page` event with the new number.
- Compact form: Newer / Older buttons around "page 1 of 16". Use it for feeds and logs where page numbers mean little.
- Show "101–120 of 312" so people know where they are. Keep the size choice to 20, 50, 100.
- Prefer filters and search over making people page; use Load more only for feeds.
