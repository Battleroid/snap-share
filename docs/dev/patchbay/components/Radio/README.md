Radios are native `<input type="radio" class="pb-radio">` in a `fieldset` with a legend; they stay round in both edge variants so they never read as checkboxes.

## Markup
- A `fieldset` (`.pb-choices`, `legend.pb-field__label`) and one `label.pb-choice` per option, all sharing a `name`.

## States
- Native `:checked`, `:disabled` and `:focus-visible`; the legend names the group.
- Round in both the soft and hard edge variants.

## Keys
- Native radio group: Tab enters the group, arrows move and select, Space selects.

## Events
None of its own; the native `change` (and `input`) from the radios.

## Do and don't
- Do: use for 2–6 exclusive options that need a hint each.
- Don't: use for short exclusive options in a toolbar; use Segmented.
- Don't: use for more than six; use Select.
