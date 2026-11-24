Media is a share card: the media itself on top, then title, subtitle, meta and actions.

## Markup
- `<article class="pb-media">` > `.pb-media__frame` + `.pb-media__body` (a `.pb-media__title`, `.pb-media__sub`, a `dl.pb-media__meta` of `dt`/`dd` pairs, and `.pb-media__actions`).
- Frames: plain (`<img>` or `<video controls>` filling 16:9; add `--contain` for screenshots), `--grid` (up to three images, first large, plus `.pb-media__more` "+3"), `--player` (a MediaPlayer with full controls; `--video` with `.pb-media__play` and `.pb-media__time` is only a static poster), `--file` (`.pb-media__fileicon`, `.pb-media__filename`, `.pb-media__filetype`, a primary Download).
- Meta labels are lowercase words (`size`, `expires`, `shared by`); values are mono.
- Compact tile: `.pb-media--compact` inside a `.pb-mediagrid` for dense grids: a 4:3 frame (`pb-media__frame--icon` centres a file icon) and a body with `pb-media__head` (`a.pb-media__name`, ellipsised), a mono `pb-media__sub` and an optional `pb-media__badges` row. A tile still being made holds the `pb-proc--tile` stand-in (see Media state).

## States
- A file card has no picture: the frame shows the icon, name and type.
- The `--video` poster is static; use `--player` for something that plays.
- A compact tile whose preview is still being made shows `pb-proc--tile` (processing or failed).

## Keys
- Native controls only: Tab and Space/Enter for links and buttons. The `--player` frame follows the MediaPlayer keys.

## Events
None; the component fires no events of its own.

## Do and don't
- Do: keep meta to four items.
- Do: one primary action per card: Download. Copy link is secondary.
- Don't: use the `--video` poster frame when the video should play; use `--player`.
