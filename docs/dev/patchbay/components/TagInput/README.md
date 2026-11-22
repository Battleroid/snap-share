TagInput collects many short values as removable chips in one field.

- Consumer provides: `span.pb-input.pb-tags` holding existing `span.pb-tag`s (text + a remove `button` with `pb-icon--close` and `aria-label`) and a trailing `<input>`.
- Script: `pbTags(el)`. Enter, comma or Tab adds; Backspace on an empty input removes the last; pasting a comma or newline list adds each; duplicates are ignored. Fires `change` on the field.
- Validate each value (CIDR, email, username) before adding and say what is allowed in the help line.
