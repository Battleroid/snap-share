Steps show progress through a fixed sequence such as setup or a restore.

- Markup: `ol.pb-steps` (`data-n="2"`…`"8"` = number of steps for even spacing; default 4) of `li.pb-step` with `pb-step__label` and optional `pb-step__desc`.
- State: `data-state="done"` (check), `data-state="current"` with `aria-current="step"`, `data-state="error"` (shows `!`, and the description says what failed). No attribute means upcoming.
- Say the state in text too, not only with the ✓ and ! marks: start the label with a visually hidden `<span class="pb-sr">completed: </span>`, `current: ` or `error: ` (upcoming needs none), so a screen reader hears "completed: Storage".
- `pb-steps--vertical` for long labels, narrow panes and job progress.
- Done steps can be links back; upcoming steps are never links. Keep it under six steps.
