The markdown editor writes formatted text for share descriptions, notes and comments.

## Markup
- `.pb-md[data-md][data-view=write|split|preview]` (`--md-h` height, set with a class or `data-vars="--md-h:320px"`) with `pb-md__bar` (toolbar buttons `[data-md-cmd=bold|italic|code|link|h|ul|ol|task|quote|block]`, `pb-md__sep`, a `[data-md-view]` radiogroup), `pb-md__panes` (a `textarea` and `div.pb-md__preview.pb-prose`) and `pb-md__foot` with `[data-md-count]`.
- `pbMdEditor(el)` (the `md` hook) wraps the selection or starts a line for each command and updates the preview as you type (debounced, about 120 ms).
- `pbMarkdown(text)` is the renderer: headings, bold, italic, strikethrough, inline and fenced code, links (http, https, relative, #), lists, task lists, quotes and rules. It escapes HTML first, so pasted markup shows as text.
- `el.pbValue()` returns the source, `el.pbHtml()` the HTML.
- Words (placeholders for inserted text, the counter) come from `pbStrings` (`mdBold`, `mdItalic`, `mdCode`, `mdText`, `mdCount`, `mdEmpty`).

## States
- `data-view` is `write`, `split` or `preview`; the view switch sets it. Split stacks the panes under 560px of container width.
- An empty preview shows the `mdEmpty` note.

## Keys
- The editor's own shortcuts (⌘/Ctrl B, I, K, E) are handled and stopped, so ⌘K inserts a link and does not also open the command palette. Hints in the button titles show ⌘ on Apple and Ctrl elsewhere.
- The command buttons are one tab stop: arrows, Home and End move between them; the view switch is its own stop.

## Events
None of its own. The textarea fires native `input`; read the text with `pbValue()`.

## Do and don't
- Do: store the markdown source and render it with `pbMarkdown`, which escapes HTML.
- Don't: insert your own HTML into the preview.
- Don't: bind ⌘K on the page for something else while the editor has focus; the editor keeps it for links.
