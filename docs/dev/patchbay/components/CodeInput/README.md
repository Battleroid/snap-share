CodeInput is one box per digit for verification and 2FA codes.

- Consumer provides: `div.pb-otp[role=group][data-codeinput]` with an `aria-labelledby` label and one `<input inputmode="numeric" maxlength="1">` per digit (`autocomplete="one-time-code"` on the first), optional `span.pb-otp__sep` between groups.
- Script: `pbOtp(el)`. Typing advances, Backspace goes back, ←→ move, pasting a whole code fills every box. When all boxes are full the field gets `data-valid` and fires a bubbling `pb:complete` event with the code in `detail`.
- Wrong code: `data-invalid` on the `.pb-field`, and a `.pb-field__error` (with a `status-down` icon) that says what to do, named by the group's `aria-describedby`. The field is a group: `span.pb-field__label[id]` and `div.pb-otp[role=group][aria-labelledby]`.
- Forms: `data-name="code"` on the group adds a hidden input of that name that always holds the whole code, so a plain form submit sends it.
