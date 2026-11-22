Checkboxes are native `<input type="checkbox" class="pb-check">` inside a `label.pb-choice`.

- Consumer provides: `<label class="pb-choice"><input type="checkbox" class="pb-check"><span>Label<span class="pb-choice__hint">Hint</span></span></label>`. Stack several in `.pb-choices`.
- States: checked, unchecked, indeterminate (set `el.indeterminate = true` from script, for "select all" over a partial selection), `disabled`.
- Use for independent options saved with a form. For a setting that applies the moment it changes, use Switch.
