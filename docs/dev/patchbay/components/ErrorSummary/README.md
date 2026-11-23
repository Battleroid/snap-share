The error summary lists every problem in a form at the top, linked to the fields.

- Markup: a hidden `div.pb-errsum[role=alert][tabindex=-1]` with `pb-errsum__title` and an empty `ul` at the top of the form. Fields carry an `id` and `data-error="message"`.
- `pbValidate(form)` checks native constraints on submit; if any fail it fills the list with links to each field, marks the fields invalid with their message below, shows the summary and moves focus to it. Fixing a field clears its error.
- Messages say how to fix it: "Enter an email address like alex@lab.home", not "Invalid email". The same text appears in the summary and under the field.
- Use it for forms with more than two fields. Short forms only need inline errors.
