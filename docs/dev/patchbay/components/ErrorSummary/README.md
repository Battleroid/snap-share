The error summary lists every problem in a form at the top, linked to the fields.

## Markup

- `form[data-validate]` with fields in the field pattern (`div.pb-field` > `label.pb-field__label[for]` + control, see TextInput), each control with an `id` and `data-error="message"`. The summary is optional: a hidden `div.pb-errsum[role=alert][tabindex=-1]` with `pb-errsum__title` and an empty `ul` at the top of the form.
- `pbValidate(form)` (the `validate` hook) checks native constraints on submit. For each invalid control it sets `aria-invalid="true"`, `data-invalid` on its `.pb-field`, and a `.pb-field__error` with the message (escaped) that joins the control's `aria-describedby`. It fills the summary with links to the fields, shows it, and moves focus to the first invalid field (`data-validate="summary"` focuses the summary instead). Fixing a field clears its error.

## States

- Invalid: `aria-invalid="true"`, `data-invalid` on the `.pb-field`, an error line under the field and a link in the summary; the same text appears in both.
- Valid: no summary, no errors. A form section (`data-section`) stays dirty, not busy, after an invalid submit.

## Keys

- A link in the summary focuses its field. The summary is `role="alert"` with `tabindex="-1"` so it can take focus.

## Events

- `pb:invalid` bubbles from the form and is cancelable, with `detail: {fields, submitter}`; cancel it to show errors your own way. It fires after the native submit is prevented.
- The component does not stop other submit listeners. Your own submit handler still runs, so check `e.pbInvalid` (the invalid controls, `[]` when the form is valid) before you send: `form.addEventListener('submit', (e) => { e.preventDefault(); if (e.pbInvalid.length) return; send(); })`.

## Do and don't

- Do: write messages that say how to fix it: "Enter an email address like alex@lab.home", not "Invalid email".
- Do: use it for forms with more than two fields.
- Don't: add a summary to short forms; inline errors are enough.
