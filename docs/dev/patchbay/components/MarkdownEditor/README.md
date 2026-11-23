The markdown editor writes formatted text for share descriptions, notes and comments.

- Markup: `.pb-md[data-md][data-view=write|split|preview]` (`--md-h` height) with `pb-md__bar` (toolbar buttons `[data-md-cmd=bold|italic|code|link|h|ul|ol|task|quote|block]`, `pb-md__sep`, a `[data-md-view]` radiogroup), `pb-md__panes` (a `textarea` and `div.pb-md__preview.pb-prose`) and `pb-md__foot` with `[data-md-count]`.
- `pbMdEditor(el)` wraps the selection or starts a line for each command, supports ⌘B ⌘I ⌘K ⌘E, and updates the preview as you type. Split stacks under 560px.
- `pbMarkdown(text)` is the renderer: headings, bold, italic, strikethrough, inline and fenced code, links (http, https, relative, #), lists, task lists, quotes and rules. It escapes HTML first, so pasted markup shows as text.
- `el.pbValue()` returns the source, `el.pbHtml()` the HTML.
