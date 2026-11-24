Text inputs pair a mono field with an uppercase label and an italic serif helper line.

- Consumer provides (the field pattern, used by every single-control field):
  ```html
  <div class="pb-field">
    <label class="pb-field__label" for="host">Hostname</label>
    <span class="pb-input"><input id="host" aria-describedby="host-help"></span>
    <span class="pb-field__help" id="host-help">Lowercase, no dots.</span>
  </div>
  ```
  The label names the control through `for`, and `aria-describedby` reads the help (and the error, below) after the name. **Changed:** `<label class="pb-field">` wrapping everything made the help text part of the control's name; it is gone. Checkbox, radio and switch rows keep their wrapping `label.pb-choice`, which is right for them.
- Several controls under one label (code input, date range, dual range, key/value, date and time): `div.pb-field[role=group][aria-labelledby=L]` with `span.pb-field__label#L`, each control with its own `aria-label`.
- Inside `.pb-input`: a leading `pb-icon`, a `.pb-input__affix` (`https://`); put the affix after the input for a trailing unit (`opens`, `GB`); a trailing `pb-kbd` hint. The box takes the focus ring.
- Error: `data-invalid` on `.pb-field`, `aria-invalid="true"` on the control, and `<span class="pb-field__error" id="port-error"><span class="pb-icon pb-icon--status-down pb-icon--16" aria-hidden="true"></span>Port must be 1–65535.</span>` added to its `aria-describedby`. Write the fix, not the fault. `form[data-validate]` (ErrorSummary) does all of this for you on submit: it escapes the message, moves focus to the first invalid field and fires a cancelable `pb:invalid`. Disabled: `data-disabled` plus `disabled` on the input.
- Values are mono because they are usually hosts, ports and paths.
- Character limit: wrap the help line and a `.pb-field__count` ("29 / 120") in `.pb-field__meta`; set `data-over` on the count past the limit. Prefer a soft limit with `data-over` to silently cutting text.
- Side by side: `.pb-form__row` lines fields up by their tops, so inputs stay level under their labels when only one of them has help text; the help hangs below.
- Native states show too: a `disabled` control inside `.pb-input` looks disabled without `data-disabled`, and a field holding `[aria-invalid="true"]` gets the error look without `data-invalid`.
