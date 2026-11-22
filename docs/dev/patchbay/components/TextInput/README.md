Text inputs pair a mono field with an uppercase label and an italic serif helper line.

- Consumer provides: `<label class="pb-field">` with `.pb-field__label`, a `.pb-input` box holding an `<input>` (or `<textarea>`), and optional `.pb-field__help`.
- Inside `.pb-input`: a leading `pb-icon`, a `.pb-input__affix` (`https://`, units), a trailing `pb-kbd` hint. The box takes the focus ring.
- Error: add `data-invalid` to `.pb-field` and write the fix in the help line with a `status-down` icon ("Port must be 1–65535."). Disabled: `data-disabled` plus `disabled` on the input.
- Values are mono because they are usually hosts, ports and paths.
