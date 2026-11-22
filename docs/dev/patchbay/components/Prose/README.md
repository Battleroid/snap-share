`pb-prose` sets long-form text — docs, notes, incident write-ups — in the serif at a 68ch measure.

- Consumer provides: a container with `pb-prose` and plain HTML inside (`h1–h3`, `p`, `ul/ol`, `a`, `code`, `pre`, `blockquote`, `hr`, `kbd`, `mark`, `small`).
- Body is `body` (17/27). Headings use `h1`/`h2`/`h3`. Inline `code` and `kbd` switch to Obel Mono. `pre` uses `code-bg`; wrap tokens in `tok-*` spans (see CodeArea) for color.
- `hr` is a `line` hairline (ink in hard). `mark` is `mark` yellow. `small` is the italic `note` style.
