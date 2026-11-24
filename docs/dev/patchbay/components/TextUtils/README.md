Text helpers show times, long names, hashes and paths in the space they have.

- Relative time: `<time class="pb-reltime" datetime="ISO">fallback</time>`; `pbRelTime(root)` writes "4 minutes ago" and sets `title` to the exact local time. It refreshes every 30 seconds. Use it for anything within a week; show dates beyond that.
- `pb-truncate` cuts one line with an ellipsis; set `title` to the full text. `pb-clamp-2` / `pb-clamp-3` clamp paragraphs; pair with a `button.pb-more` whose `aria-controls` names the paragraph (`pbClampToggle(btn)` toggles Show more / Show less).
- Middle truncation: `<span class="pb-midtrunc" data-midtrunc data-keep="12">long text</span>`; `pbMidTrunc(el)` keeps the last `data-keep` characters visible so hashes, file names and paths stay distinct. Full text goes into `title`.
- Never truncate the only copy of an error or an identifier someone has to type; give a copy button.
- `.pb-midtrunc` takes the surrounding font; add `pb-midtrunc--mono` for hosts, paths and IDs.
- Middle truncation: copying any part of a `[data-midtrunc]` value copies the whole value, not the two halves.
- Relative time: `time.pb-reltime` is focusable and shows the exact time as a tooltip (`pbTip`) instead of a `title`; inside a link, button, tab, option or menu item it is not a tab stop of its own (the tooltip still shows on hover).
