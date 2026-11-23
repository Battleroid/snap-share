Terminal output shows what a command printed, with its exit status.

- Markup: `.pb-term[data-term]` with `pb-term__head` (`pb-term__cmd`, `pb-term__meta` for duration, `pb-term__exit` with `data-status="down"` for non-zero or `busy` while running, `[data-term-copy]`, `[data-term-wrap]`) and `pre.pb-term__body`.
- `pbTerm(el)` turns ANSI color codes (`\e[31m` or the real escape) into `ansi-*` spans: bold, dim, italic, underline, the eight colors and backgrounds. `el.pbWrite(text)` appends output and stays scrolled to the end; keep a `pb-term__cursor` while running.
- Always on the `night` ground in both themes; colors are the `term-*` tokens, all 7:1 or better on it.
- Show the exit code as a word and an icon, not color alone. `--term-h` sets the height.
- The output box is focusable (`tabindex="0"`, `role="region"`, named "Terminal output" unless you name it) so keyboard users can scroll it.
