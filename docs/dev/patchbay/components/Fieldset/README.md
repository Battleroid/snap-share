Fieldset groups a form section under a legend, and the section's actions always sit inside it, never floating below.

- Consumer provides: `form.pb-form` > `fieldset.pb-fieldset` (with `legend`, optional `p.pb-fieldset__desc`) holding `.pb-field`s, `.pb-form__row` for side-by-side fields and `output.pb-output` for computed values.
- One field and one action (role, name, toggle with save): `.pb-inline` puts the field and its button on one row, the button bottom-aligned with the input; help text goes after the row.
- Several fields: end the fieldset with `.pb-fieldset__foot`, a `surface-sunk` strip across the bottom of the panel: `.pb-foot__status` on the left, then ghost Discard and the primary Save on the right.
- Status: `No changes` (buttons disabled) → `data-state="dirty"` "Unsaved changes" (buttons enabled) → `data-state="saved"` with `status-ok` "Saved just now", or `data-state="error"` with the reason.
- Card sections (danger zone, summaries) use `.pb-card__foot` the same way.
- Each section saves on its own. A page with one long form uses a single footer on its last section.
