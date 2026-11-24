Text inputs pair a mono field with an uppercase label and an italic serif helper line.

## Markup

- The field pattern, used by every single-control field:
  ```html
  <div class="pb-field">
    <label class="pb-field__label" for="host">Hostname</label>
    <span class="pb-input"><input id="host" aria-describedby="host-help"></span>
    <span class="pb-field__help" id="host-help">Lowercase, no dots.</span>
  </div>
  ```
  The label names the control through `for`, and `aria-describedby` reads the help (and the error) after the name. Do not wrap everything in `<label class="pb-field">`: that makes the help text part of the control's name. Checkbox, radio and switch rows keep their wrapping `label.pb-choice`, which is right for them.
- Several controls under one label (code input, date range, dual range, key/value, date and time): `div.pb-field[role=group][aria-labelledby=L]` with `span.pb-field__label#L`, each control with its own `aria-label`.
- Inside `.pb-input`: a leading `pb-icon`, a `.pb-input__affix` (`https://`); put the affix after the input for a trailing unit (`opens`, `GB`); a trailing `pb-kbd` hint. The box takes the focus ring.
- Character limit: wrap the help line and a `.pb-field__count` ("29 / 120") in `.pb-field__meta`; set `data-over` on the count past the limit.
- Side by side: `.pb-form__row` lines fields up by their tops, so inputs stay level under their labels when only one of them has help text; the help hangs below.
- Values are mono because they are usually hosts, ports and paths.

## States

- Error: `data-invalid` on `.pb-field`, `aria-invalid="true"` on the control, and `<span class="pb-field__error" id="port-error"><span class="pb-icon pb-icon--status-down pb-icon--16" aria-hidden="true"></span>Port must be 1–65535.</span>` added to its `aria-describedby`.
- Disabled: `data-disabled` plus `disabled` on the input.
- Native states show too: a `disabled` control inside `.pb-input` looks disabled without `data-disabled`, and a field holding `[aria-invalid="true"]` gets the error look without `data-invalid`.

## Keys

- The label names the control through `for`; the help and the error are read after the name through `aria-describedby`.
- `form[data-validate]` (ErrorSummary) moves focus to the first invalid field on submit.

## Events

- Native `input` and `change`.
- `form[data-validate]` does the error markup for you on submit (it escapes the message) and fires a cancelable `pb:invalid` on the form.

## Do and don't

- Do: write the fix, not the fault, in the error.
- Do: prefer a soft limit with `data-over` to silently cutting text.
