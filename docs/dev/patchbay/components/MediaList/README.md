MediaList lists videos with a thumbnail on the left and details on the right.

- Markup: `ul.pb-medialist` > `li.pb-mitem` with three parts:
  - `a.pb-mitem__thumb` (16:9) holding an `img`, `.pb-mitem__play` (play icon shown on hover and focus), `.pb-mitem__dur` (duration), an optional `.pb-mitem__seen` bar (`--value: 62%` watched) and an optional `pb-badge.pb-mitem__state` for processing states.
  - `.pb-mitem__body`: `a.pb-mitem__title`, `.pb-mitem__meta` (spans joined with dots: resolution and codec, size, date, views), `p.pb-mitem__desc` (clamped to two lines), `.pb-mitem__tags` of `pb-pill`, `.pb-mitem__links` (share page, original, subtitles).
  - `.pb-mitem__actions`: icon buttons (copy link, more).
- `pb-medialist--compact` shows one line per video with a smaller thumbnail and hides the description, tags and links.
- Responsive by container width: under 620px the thumbnail shrinks and actions move under the text; under 420px the thumbnail sits on top.
- Use FileBrowser for mixed files and Media for a single shared item; MediaList is for browsing many videos.
