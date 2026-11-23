TagInput collects many short values as removable chips in one field.

- Consumer provides: `span.pb-input.pb-tags` holding existing `span.pb-tag`s (text + a remove `button` with `pb-icon--close` and `aria-label`) and a trailing `<input>`.
- Script: `pbTags(el)`. Enter, comma or Tab adds; Backspace on an empty input removes the last; pasting a comma or newline list adds each; duplicates are ignored. Fires `change` on the field.
- Validate each value (CIDR, email, username) before adding and say what is allowed in the help line.
- Forms: give the box `data-name="labels"` (or put your own `input[type=hidden]` / `[data-tags-input]` inside) and the tags are kept in that hidden field joined by `data-sep` (default `,`), so a plain form submits them; a value already in the hidden field becomes the starting tags. `el.pbSet(["a","b"])` (or `"a,b"`) replaces the tags, `el.pbValues()` reads them; both adding and removing fire `change`.
