The log viewer shows a service's log as it streams, with filtering by text and level.

- Markup: `.pb-log[data-log]` with a `pb-toolbar` (search `[data-log-search]`, level toggles `[data-log-level]` checkboxes valued `debug|info|warn|error`, a Follow switch `[data-log-follow]`, wrap `[data-log-wrap]`, pause `[data-log-pause]`), `.pb-log__body[role=log]` of `.pb-log__line[data-level]` (`pb-log__time`, `pb-log__lvl`, `pb-log__msg`), and `.pb-log__foot` with `pb-log__live` and `[data-log-count]`.
- `pbLog(el)` filters and highlights matches, keeps the view pinned to the newest line while Follow is on, and shows a "N new" button when you have scrolled up. `el.pbAppend({time, level, msg})` adds a line; `data-max` (default 2000) caps how many stay in the page.
- Errors get a tinted row; debug text is muted. Levels are always words, never color alone.
- `--log-h` sets the height. For very long histories page from the server or use VirtualList.
