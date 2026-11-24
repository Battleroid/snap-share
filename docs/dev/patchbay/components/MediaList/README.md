MediaList lists videos with a thumbnail on the left and details on the right.

## Markup
- `ul.pb-medialist` > `li.pb-mitem` with three parts:
  - `a.pb-mitem__thumb` (16:9) holding an `img`, `.pb-mitem__play` (play icon shown on hover and focus), `.pb-mitem__dur` (duration), an optional `.pb-mitem__seen` bar (`aria-hidden`) holding `<span data-vars="--value:62%">` for the watched share, and an optional `pb-badge.pb-mitem__state` for processing states.
  - `.pb-mitem__body`: `a.pb-mitem__title`, `.pb-mitem__meta` (spans joined with dots: resolution and codec, size, date, views), `p.pb-mitem__desc` (clamped to two lines), `.pb-mitem__tags` of `pb-pill`, `.pb-mitem__links` (share page, original, subtitles).
  - `.pb-mitem__actions`: icon buttons (copy link, more).
- No inline `style`: the watched width goes through `data-vars` (custom properties only, set through the CSSOM), or `data-style` plus `pbStyled(container)` for markup you build as a string.

## States
- `pb-medialist--compact` shows one line per video with a smaller thumbnail (128px) and hides the description, tags and links.
- Responsive by container width: under 620px the thumbnail shrinks (148px), the description clamps to one line and actions move under the text; under 420px the thumbnail sits on top (compact keeps a 112px thumbnail beside the text and hides its actions).
- A video still being processed shows a `pb-mitem__state` badge on the thumbnail.

## Keys
- Native links and buttons only: Tab and Enter. Give the thumbnail link an `aria-label` ("Play …"), since it holds no text.

## Events
None; the component fires no events of its own.

## Do and don't
- Do: use FileBrowser for mixed files and Media for a single shared item.
- Do: use MediaList for browsing many videos.
- Don't: write the watched width as `style="..."`.
