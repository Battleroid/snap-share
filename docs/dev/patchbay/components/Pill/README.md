Pills are compact, rounded (soft) or squared (hard) labels for filters and tags.

- Filter: `<button class="pb-pill" aria-pressed="true|false">` with an optional `.pb-pill__count`. Pressed fills `accent`.
- Removable tag: `<span class="pb-pill">` with a `button.pb-pill__x` holding `pb-icon--close` and an `aria-label`.
- Pop tags: `pb-pill--yellow`, `--mint`, `--red` for a handful of fixed categories (beta, public, deprecated). Not for status; use Badge.
- Text is lowercase mono, one or two words.
