CodeInput is one box per digit for verification and 2FA codes.

- Consumer provides: `div.pb-otp[role=group]` with an `aria-labelledby` label and one `<input inputmode="numeric" maxlength="1">` per digit (`autocomplete="one-time-code"` on the first), optional `span.pb-otp__sep` between groups.
- Script: `pbOtp(el)`. Typing advances, Backspace goes back, ←→ move, pasting a whole code fills every box. When all boxes are full the field gets `data-valid` and fires a `complete` event with the code.
- Wrong code: `data-invalid` on the `.pb-field` and a `status-down` help line that says what to do.
