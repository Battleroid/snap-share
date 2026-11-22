TimePicker opens an hours and minutes picker from a time field, used instead of the browser's own time popup, which pages cannot style.

- Consumer provides: `<span class="pb-input pb-timefield">` holding `<input type="text" inputmode="numeric" placeholder="hh:mm">` and `<button type="button" class="pb-input__btn" data-clock aria-label="Open time picker">` with `pb-icon--clock`.
- Options on the field: `data-step` (minutes, default 5), `data-hour12` (12-hour with an AM/PM column; the input shows `10:30 PM`), `data-min` / `data-max` (`18:00`; hours and minutes outside are struck through and disabled).
- Script: `pbTimeField(el)` from the pickers script (it ships with DatePicker). `pbClock({value, step, hour12, min, max, onChange})` returns a bare picker for inline use (`pb-time--inline`).
- Behavior: typing works and is normalized on blur (`9:5` style input is rejected and flagged). Picking an hour keeps the minute and moves focus to minutes; picking a minute closes. ↑↓ move, ←→ switch columns, Enter picks, Esc closes. Now rounds to the step.
- Date and time together: put a `.pb-datefield` and a `.pb-timefield` in `div.pb-datetime`.
