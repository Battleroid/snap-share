Text helpers show times, long names, hashes and paths in the space they have.

## Markup

- Relative time: `<time class="pb-reltime" datetime="ISO">fallback</time>`; `pbRelTime(root)` writes "4 minutes ago". It refreshes every 30 seconds.
- `pb-truncate` cuts one line with an ellipsis; set `title` to the full text. `pb-clamp-2` / `pb-clamp-3` clamp paragraphs; pair with a `button.pb-more` whose `aria-controls` names the paragraph (`pbClampToggle(btn)` toggles Show more / Show less).
- Middle truncation: `<span class="pb-midtrunc" data-midtrunc data-keep="12">long text</span>`; `pbMidTrunc(el)` keeps the last `data-keep` characters visible so hashes, file names and paths stay distinct. Full text goes into `title`. `.pb-midtrunc` takes the surrounding font; add `pb-midtrunc--mono` for hosts, paths and IDs.

## States

- Relative time shows the exact local time as a tooltip (`pbTip`), not a `title`: the script moves any `title` into `data-tip`.
- Copying any part of a `[data-midtrunc]` value copies the whole value, not the two halves.

## Keys

- `time.pb-reltime` is focusable so the exact time is reachable by keyboard; inside a link, button, tab, option or menu item it is not a tab stop of its own (the tooltip still shows on hover).

## Events

None; the component fires no events of its own.

## Do and don't

- Do: use relative time for anything within a week; show dates beyond that.
- Don't: truncate the only copy of an error or an identifier someone has to type; give a copy button.
