Empty and result states explain an empty list, a search with no matches, or a page that failed.

- Empty: `.pb-empty` with `pb-empty__icon`, `h3.pb-empty__title`, `pb-empty__body` and `pb-empty__actions`. Say what goes here and give the one action that fills it. `pb-empty--compact` for no-results inside a panel.
- Result pages: `.pb-result[data-status]` with `pb-result__code` (404, 500), `pb-result__title`, `pb-result__body`, `pb-result__actions` and `pb-result__detail` (a reference id people can quote). The code takes the status color.
- Write the title as what happened ("That share is gone"), the body as what to do next. No apologies, no jokes.
- For 500s offer Try again; for 404s link home and to the status page.
