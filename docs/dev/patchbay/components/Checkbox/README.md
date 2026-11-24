Checkboxes are native `<input type="checkbox" class="pb-check">` inside a `label.pb-choice`.

## Markup

- `<label class="pb-choice"><input type="checkbox" class="pb-check"><span>Label<span class="pb-choice__hint">Hint</span></span></label>`. Stack several in `.pb-choices`.
- `data-indeterminate` on the input sets the indeterminate state at load (the `indeterminate` hook).

## States

Checked, unchecked, indeterminate (`data-indeterminate`, or `el.indeterminate = true` from script, for "select all" over a partial selection), `disabled`.

## Keys

Native checkbox: Tab to focus, Space to toggle.

## Events

Native `input` and `change` events only; the component adds none.

## Do and don't

- Do: use it for independent options saved with a form.
- Don't: use it for a setting that applies the moment it changes; use Switch.
