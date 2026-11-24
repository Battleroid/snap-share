Fieldset groups a form section under a legend, and the section's actions always sit inside it, never floating below.

## Markup

- Consumer provides: `form.pb-form` > `fieldset.pb-fieldset` (with `legend`, optional `p.pb-fieldset__desc`) holding `.pb-field`s (`div.pb-field` > `label.pb-field__label[for]`, the control with `aria-describedby` for its help and error, `.pb-field__help`; see TextInput), `.pb-form__row` for side-by-side fields and `output.pb-output` for computed values.
- One field and one action (role, name, toggle with save): `.pb-inline` puts the field and its button on one row, the button bottom-aligned with the input; help text goes after the row.
- Several fields: end the fieldset with `.pb-fieldset__foot`, a `surface-sunk` strip across the bottom of the panel: `.pb-foot__status` on the left, then ghost Discard (`type=reset`) and the primary Save on the right.
- Script: `form[data-section]` (or a container holding one form). On bind it snapshots every control's value, named or not.
- Card sections (danger zone, summaries) use `.pb-card__foot` the same way.

## States

- A clean settings section deliberately starts with Discard and Save disabled and a "No changes" status: it is a settings pattern, with nothing to save until something changes. A one-shot form (create, sign-in, invite) is different: keep its submit enabled until the request starts, then show busy.
- Status: `No changes` (buttons disabled) → `data-state="dirty"` "Unsaved changes" (buttons enabled) → `data-state="busy"` "Saving…" → `data-state="saved"` with `status-ok` "Saved just now", or `data-state="error"` with the reason.
- Input that differs from the snapshot makes it dirty; input that returns to the snapshot, or Discard, makes it clean again, and Discard restores the snapshot. Other buttons (reveal, copy, danger) are left alone.
- On bind a clean section disables Discard and Save even if the markup forgot to; a server-rendered `data-state="error"` (or `dirty`) leaves them enabled so the user can retry.
- Server-rendered state: a `.pb-foot__status[data-state=saved|error]` from the server stays until the next input. A section that holds `[data-invalid]` starts dirty so the user can fix and resubmit.
- Leaving the page: when the app patterns are loaded (`pbGuardUnload`), a dirty section asks before the page unloads; a save in flight does not count.

## Keys

- Native controls only: Tab and Space/Enter as for each element. The `legend` names the group.

## Events

- Submit sets busy. The section does not send anything; the app does, then settles it with `form.dispatchEvent(new CustomEvent('pb:saved'))` (takes a new snapshot; optional `detail.message`) or `new CustomEvent('pb:error', {detail: {message}})`.
- Call `preventDefault()` on submit yourself if you save with `fetch`. With `data-validate` too, an invalid submit leaves the section dirty (check `e.pbInvalid` before you send).

## Do and don't

- Do: keep the section's actions inside it, never floating below.
- Do: let each section save on its own; a page with one long form uses a single footer on its last section.
- Don't: leave the section stuck in busy; always settle it with `pb:saved` or `pb:error`.
