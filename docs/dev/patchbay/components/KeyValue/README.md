KeyValue edits a list of key and value pairs: environment variables, headers, labels.

- Consumer provides: `div.pb-kv` > `.pb-kv__head` (key, value labels) > `.pb-kv__row`s (key `.pb-input`, value `.pb-input`, a secret toggle `pb-btn--icon[aria-pressed][data-secret]` with `pb-icon--lock`, a ghost remove button `[data-remove]`) > `button.pb-kv__add[data-add]`.
- Script: Add clones an empty row and focuses it; remove moves focus to the next row; the secret toggle switches the value to a password field; keys are uppercased with `_` for env vars (drop that rule for headers).
- Put it inside a Fieldset with a footer, since it is saved as a whole.
