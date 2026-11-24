CodeInput is one box per digit for verification and 2FA codes.

## Markup

- `div.pb-otp[role=group][data-codeinput]` with an `aria-labelledby` label and one `<input inputmode="numeric" maxlength="1">` per digit (`autocomplete="one-time-code"` on the first), optional `span.pb-otp__sep` between groups.
- Inside a field: `span.pb-field__label[id]` and the group in `.pb-field`; a wrong code adds `data-invalid` on the `.pb-field` and a `.pb-field__error` (with a `status-down` icon) that says what to do, named by the group's `aria-describedby`.
- Forms: `data-name="code"` on the group adds a hidden input of that name that always holds the whole code, so a plain form submit sends it.

## States

- Full: when all boxes are filled the field gets `data-valid`.
- Wrong code: `data-invalid` on the `.pb-field`.

## Keys

Typing advances, Backspace on an empty box goes back, ←/→ move, focus selects the box's digit, pasting a whole code fills every box. Digits only.

## Events

A bubbling `pb:complete` on the group when every box is filled, `detail` the code string.

## Do and don't

- Do: say what to do in the error text, not just that the code is wrong.
- Do: use `data-name` so a plain form submit sends the whole code.
- Don't: use it for codes that are not digits.
