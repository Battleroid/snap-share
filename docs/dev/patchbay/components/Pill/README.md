Pills are compact, rounded (soft) or squared (hard) labels for filters and tags.

## Markup
- Filter: `<button class="pb-pill" aria-pressed="true|false">` with an optional `.pb-pill__count`.
- Removable tag: `<span class="pb-pill">` with a `button.pb-pill__x` holding `pb-icon--close` and an `aria-label`.
- Pop tags: `pb-pill--yellow`, `--mint`, `--red` for a handful of fixed categories (beta, public, deprecated).
- Hued tags: `span.pb-pill[data-hue="1"…"6"]` gives a user-defined tag one of six quiet tints (chart and pop tokens mixed into `surface`, text in `ink`; both themes pass contrast). Pick the hue from a stable hash of the tag name, e.g. `1 + (sum of char codes % 6)`, so a tag keeps its colour everywhere.
- Text is lowercase mono, one or two words. Inside a table cell or a tile a pill centres on the line of text beside it.

## States
- Pressed fills `accent`; a pressable pill also has hover and focus states.
- Pop and hued tags are display only.

## Keys
- A filter pill is a native button: Tab, then Space/Enter toggles `aria-pressed`. Your code flips the attribute.
- A removable tag's `pb-pill__x` is its own button and needs an `aria-label` naming what it removes.

## Events
None; the component fires no events of its own. Listen for `click` on the pill or its remove button.

## Do and don't
- Do: use a hued pill only for display tags people create.
- Don't: use pop colors or hues for status; use Badge.
- Don't: use a hued pill as a filter; use the pressable pill.
