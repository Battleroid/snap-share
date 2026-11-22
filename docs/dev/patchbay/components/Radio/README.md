Radios are native `<input type="radio" class="pb-radio">` in a `fieldset` with a legend; they stay round in both edge variants so they never read as checkboxes.

- Consumer provides: a `fieldset` (`.pb-choices`, `legend.pb-field__label`) and one `label.pb-choice` per option, all sharing a `name`.
- Use for 2–6 exclusive options that need a hint each. For short exclusive options in a toolbar, use Segmented. For more than six, use Select.
