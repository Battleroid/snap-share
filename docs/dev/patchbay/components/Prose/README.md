`pb-prose` sets long-form text — docs, notes, incident write-ups — in the serif at a 68ch measure.

## Markup
- A container with `pb-prose` and plain HTML inside (`h1–h3`, `p`, `ul/ol`, `a`, `code`, `pre`, `blockquote`, `hr`, `kbd`, `mark`, `small`).
- Body is `body` (17/27). Headings use `h1`/`h2`/`h3`. Inline `code` and `kbd` switch to Obel Mono. `pre` uses `code-bg`; wrap tokens in `tok-*` spans (see CodeArea) for color.
- `hr` is a `line` hairline (ink in hard). `mark` is `mark` yellow. `small` is the italic `note` style.
- Tables inside `.pb-prose` get hairline rules, an uppercase mono header and an optional bottom caption; give number cells `class="pb-num"` for right-aligned mono figures.

## States
- Both themes and both edges are covered.
- A table wider than the column scrolls sideways inside itself.

## Keys
- The `scroll` hook makes `pre` and wide tables named, focusable scroll regions while they overflow, so they can be scrolled from the keyboard.

## Events
None; the component fires no events of its own.

## Do and don't
- Do: use plain HTML elements, not extra classes, inside the container.
- Do: markdown from `pbMarkdown` belongs here (the editor preview uses it).
- Don't: use `pb-prose` for app chrome; it is for reading text.
