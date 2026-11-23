Fieldset groups a form section under a legend, and the section's actions always sit inside it, never floating below.

- Consumer provides: `form.pb-form` > `fieldset.pb-fieldset` (with `legend`, optional `p.pb-fieldset__desc`) holding `.pb-field`s, `.pb-form__row` for side-by-side fields and `output.pb-output` for computed values.
- One field and one action (role, name, toggle with save): `.pb-inline` puts the field and its button on one row, the button bottom-aligned with the input; help text goes after the row.
- Several fields: end the fieldset with `.pb-fieldset__foot`, a `surface-sunk` strip across the bottom of the panel: `.pb-foot__status` on the left, then ghost Discard and the primary Save on the right.
- Status: `No changes` (buttons disabled) → `data-state="dirty"` "Unsaved changes" (buttons enabled) → `data-state="busy"` "Saving…" → `data-state="saved"` with `status-ok` "Saved just now", or `data-state="error"` with the reason.
- Script: `form[data-section]` (or a container holding one form). On bind it snapshots every control's value, named or not. Input that differs from the snapshot makes it dirty and enables Discard (`type=reset`) and Save (submit buttons); input that returns to the snapshot, or Discard, makes it clean again, and Discard restores the snapshot. Other buttons (reveal, copy, danger) are left alone.
- Saving: submit sets busy. The section does not send anything; the app does, then settles it with `form.dispatchEvent(new CustomEvent('pb:saved'))` (takes a new snapshot; optional `detail.message`) or `new CustomEvent('pb:error', {detail: {message}})`. Call `preventDefault()` on submit yourself if you save with `fetch`.
- Server-rendered state: a `.pb-foot__status[data-state=saved|error]` from the server stays until the next input. A section that holds `[data-invalid]` starts dirty so the user can fix and resubmit.
- Card sections (danger zone, summaries) use `.pb-card__foot` the same way.
- Each section saves on its own. A page with one long form uses a single footer on its last section.
- On bind a clean section disables Discard and Save even if the markup forgot to; a server-rendered `data-state="error"` (or `dirty`) leaves them enabled so the user can retry.
