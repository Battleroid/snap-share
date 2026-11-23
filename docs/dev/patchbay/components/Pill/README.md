Pills are compact, rounded (soft) or squared (hard) labels for filters and tags.

- Filter: `<button class="pb-pill" aria-pressed="true|false">` with an optional `.pb-pill__count`. Pressed fills `accent`.
- Removable tag: `<span class="pb-pill">` with a `button.pb-pill__x` holding `pb-icon--close` and an `aria-label`.
- Pop tags: `pb-pill--yellow`, `--mint`, `--red` for a handful of fixed categories (beta, public, deprecated). Not for status; use Badge.
- Text is lowercase mono, one or two words.
- Hued tags: `span.pb-pill[data-hue="1"…"6"]` gives a user-defined tag one of six quiet tints (chart and pop tokens mixed into `surface`, text in `ink`; both themes pass contrast). Pick the hue from a stable hash of the tag name, e.g. `1 + (sum of char codes % 6)`, so a tag keeps its colour everywhere. Only for display tags people create; never for status (use Badge) or for filters (use the pressable pill).
- Inside a table cell or a tile a pill centres on the line of text beside it.
