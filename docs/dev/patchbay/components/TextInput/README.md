Text inputs pair a mono field with an uppercase label and an italic serif helper line.

- Consumer provides: `<label class="pb-field">` with `.pb-field__label`, a `.pb-input` box holding an `<input>` (or `<textarea>`), and optional `.pb-field__help`.
- Inside `.pb-input`: a leading `pb-icon`, a `.pb-input__affix` (`https://`); put the affix after the input for a trailing unit (`opens`, `GB`); a trailing `pb-kbd` hint. The box takes the focus ring.
- Error: add `data-invalid` to `.pb-field` and write the fix in the help line with a `status-down` icon ("Port must be 1–65535."). Disabled: `data-disabled` plus `disabled` on the input.
- Values are mono because they are usually hosts, ports and paths.
- Character limit: wrap the help line and a `.pb-field__count` ("29 / 120") in `.pb-field__meta`; set `data-over` on the count past the limit. Prefer a soft limit with `data-over` to silently cutting text.
- Side by side: `.pb-form__row` lines fields up by their tops, so inputs stay level under their labels when only one of them has help text; the help hangs below.
