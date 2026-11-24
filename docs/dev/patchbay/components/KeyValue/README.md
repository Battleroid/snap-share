KeyValue edits a list of key and value pairs: environment variables, headers, labels.

## Markup

- Consumer provides: `div.pb-kv[data-kv]` > `.pb-kv__head` (key, value labels) > `.pb-kv__row`s (key `.pb-input`, value `.pb-input`, a secret toggle `pb-btn--icon[aria-pressed][data-secret]` with `pb-icon--lock`, a ghost remove button `[data-remove]`) > `button.pb-kv__add[data-add]`. The first row is the template Add clones, so keep one.
- Put it inside a Fieldset with a footer, since it is saved as a whole.

## States

- The secret toggle (`aria-pressed`) switches the value to a password field.
- Keys are uppercased with `A-Z`, `0-9` and `_` only (right for env vars; this rule is built in, so headers or labels with other characters need their own control).

## Keys

- Add clones an empty row and focuses its key; remove moves focus to the next row's key, or to Add when it was the last. A row's remove button is named after its key ("Remove TZ").

## Events

- None of its own; the native `input` events from the fields bubble to the form, which a Fieldset watches for changes.

## Do and don't

- Do: put it inside a Fieldset with a footer.
