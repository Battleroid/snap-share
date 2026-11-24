ChoiceCard shows radio or checkbox options as panels, for choices that need a description, a figure or an icon.

## Markup

- `fieldset.pb-choicecards` with a `legend.pb-field__label` of `label.pb-choicecard`s, each with an `input` (`pb-radio` or `pb-check`, shown top-right), optional `pb-icon--24`, `.pb-choicecard__title`, `.pb-choicecard__desc` and `.pb-choicecard__meta`.
- The legend sits 6px above the cards like any field label, and the group keeps the form gap to the next field. `fieldset.pb-choices` (Radio) gets the same legend spacing.
- Lay out 2–4 cards in a row.

## States

- Checked: `accent` outline (soft) or a 4px `accent` offset shadow (hard).
- Disabled: `surface-sunk` and muted text; say why in the description.

## Keys

Native radio or checkbox inside each card: arrows move a radio group, Space toggles a checkbox.

## Events

Native `input` and `change` events only.

## Do and don't

- Do: use it for choices that need a description, a figure or an icon.
- Don't: use it for short options without detail; use Radio or Segmented.
