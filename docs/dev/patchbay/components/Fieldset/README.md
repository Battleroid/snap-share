Fieldset groups related fields under a legend inside a form; `.pb-form` spaces the stack.

- Consumer provides: `form.pb-form` > `fieldset.pb-fieldset` (with `legend`) holding `.pb-field`s, `.pb-form__row` for side-by-side fields, optional `output.pb-output` for computed values, then `.pb-form__actions` (ghost Reset, primary Save) after the fieldset.
- `disabled` on the fieldset disables every control inside.
- One primary action per form, last in the row.
