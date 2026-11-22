Media is a share card: the media itself on top, then title, subtitle, meta and actions.

- Consumer provides: `<article class="pb-media">` > `.pb-media__frame` + `.pb-media__body` (a `.pb-media__title`, `.pb-media__sub`, a `dl.pb-media__meta` of `dt`/`dd` pairs, and `.pb-media__actions`).
- Frames: plain (`<img>` or `<video controls>` filling 16:9; add `--contain` for screenshots), `--grid` (up to three images, first large, plus `.pb-media__more` "+3"), `--video` (poster with `.pb-media__play` and `.pb-media__time`), `--file` (`.pb-media__fileicon`, `.pb-media__filename`, `.pb-media__filetype`, a primary Download).
- Meta labels are lowercase words (`size`, `expires`, `shared by`); values are mono. Keep to four.
- One primary action per card: Download. Copy link is secondary.
