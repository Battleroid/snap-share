TagInput collects many short values as removable chips in one field.

## Markup

- `span.pb-input.pb-tags` holding existing `span.pb-tag`s (text + a remove `button` with `pb-icon--close` and `aria-label`) and a trailing `<input>`. Script: `pbTags(el)`.
- Forms: give the box `data-name="labels"` (or put your own `input[type=hidden]` / `[data-tags-input]` inside) and the tags are kept in that hidden field joined by `data-sep` (default `,`), so a plain form submits them; a value already in the hidden field becomes the starting tags. `el.pbSet(["a","b"])` (or `"a,b"`) replaces the tags, `el.pbValues()` reads them.
- Say what is allowed in the help line.

## States

- `data-pattern="regex"` (anchored) marks each tag that fails it (`data-invalid`, a dashed red chip, "invalid" for screen readers), sets `aria-invalid` and a custom validity on the input, and shows `data-pattern-error` (or `pbStrings.tagInvalid`) as the field's error until the tag is removed.
- Duplicates are ignored.
- Text typed but not yet committed (no Enter or comma) becomes a tag when the input loses focus and before its form submits, so `pbValues()` and the hidden field include it.

## Keys

- Enter, comma or Tab adds; Backspace on an empty input removes the last; pasting a comma or newline list adds each.
- Each remove button has an `aria-label`.

## Events

- Native `change` on the field, for both adding and removing. No `pb:` event.

## Do and don't

- Do: validate each value (CIDR, email, username) before adding it.
