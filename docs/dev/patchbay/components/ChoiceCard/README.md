ChoiceCard shows radio or checkbox options as panels, for choices that need a description, a figure or an icon.

- Consumer provides: `fieldset.pb-choicecards` (with a `legend`) of `label.pb-choicecard`s, each with an `input` (`pb-radio` or `pb-check`, shown top-right), optional `pb-icon--24`, `.pb-choicecard__title`, `.pb-choicecard__desc` and `.pb-choicecard__meta`.
- Checked: `accent` outline (soft) or a 4px `accent` offset shadow (hard). Disabled: `surface-sunk` and muted text; say why in the description.
- 2–4 cards in a row. For short options without detail, use Radio or Segmented.
- Group: `fieldset.pb-choicecards` with a `legend.pb-field__label`; the legend sits 6px above the cards like any field label, and the group keeps the form gap to the next field. `fieldset.pb-choices` (Radio) gets the same legend spacing.
