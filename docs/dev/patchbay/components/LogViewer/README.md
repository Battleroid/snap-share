The log viewer shows a service's log as it streams, with filtering by text and level.

## Markup
- `.pb-log[data-log]` with a `pb-toolbar` (search `[data-log-search]`, a group `[data-log-level]` holding level checkboxes valued `debug|info|warn|error`, a Follow switch `[data-log-follow]`, wrap `[data-log-wrap]`, pause `[data-log-pause]`), `.pb-log__body[role=log]` of `.pb-log__line[data-level]` (`pb-log__time`, `pb-log__lvl`, `pb-log__msg`), and `.pb-log__foot` with `pb-log__live` and `[data-log-count]`.
- `pbLog(el)` (run by the `log` hook on `[data-log]`) filters and highlights matches and keeps the view pinned to the newest line while Follow is on. `el.pbAppend({time, level, msg, src})` adds a line; `data-max` (default 2000) caps how many stay in the page.
- Height is `--log-h` (default 320px): set it with a class, or `data-vars="--log-h:240px"`. Never an inline `style`.
- For very long histories page from the server or use VirtualList.

## States
- Follow on: the view stays pinned to the newest line; scrolled up, a "N new" button appears and counts arrivals. Turning Follow on jumps to the bottom.
- Pause sets `data-paused` on the log and the button's `aria-pressed`; the footer reads paused instead of live. While paused `pbAppend` drops lines, so buffer them yourself if they matter.
- Wrap toggles `pb-log--wrap` (long lines wrap instead of scrolling sideways).
- Filtering hides non-matching lines, shows an empty line when none match, and the count reads "n of total".
- Errors get a tinted row; debug text is muted. Levels are always words, never color alone.

## Keys
- Native controls only: Tab and Space/Enter for the toolbar buttons, switch and checkboxes.
- The body is `role="log"`, so new lines are announced politely; give it an `aria-label` naming the service and `tabindex="0"` so the scrolling area can be reached from the keyboard.

## Events
None; the component fires no events of its own. Drive it with `pbAppend`; the search and level inputs fire their native `input`/`change`.

## Do and don't
- Do: show the level as a word on every line.
- Do: page long histories from the server, or use VirtualList.
- Don't: set the height with an inline `style`.
