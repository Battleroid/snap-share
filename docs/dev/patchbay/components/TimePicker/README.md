TimePicker opens an hours and minutes picker from a time field, used instead of the browser's own time popup, which pages cannot style.

## Markup

- `<span class="pb-input pb-timefield">` holding `<input type="text" inputmode="numeric" placeholder="hh:mm">` and `<button type="button" class="pb-input__btn" data-clock aria-label="Open time picker">` with `pb-icon--clock`.
- Options on the field: `data-step` (minutes, default 5), `data-hour12` (12-hour with an AM/PM column; the input shows `10:30 PM`), `data-min` / `data-max` (`18:00`).
- Script: `pbTimeField(el)` from the pickers script (it ships with DatePicker). `pbClock({value, step, hour12, min, max, onChange})` returns a bare picker for inline use (`pb-time--inline`).
- Date and time together: put a `.pb-datefield` and a `.pb-timefield` in `div.pb-datetime`.

## States

- Hours and minutes outside `data-min` / `data-max` are struck through and disabled.
- Typing works and is normalized on blur (`9:5` style input is rejected and flagged). A typed time that cannot be read shows an error line ("Enter a time as hh:mm.") through `pbFieldError`.
- Now rounds to the step.

## Keys

- Picking an hour keeps the minute and moves focus to minutes; picking a minute closes. ↑↓ move, ←→ switch columns, Enter picks, Esc closes.
- The hour, minute and AM/PM columns are listboxes named Hour, Minute and AM or PM, their times `role=option` with `aria-selected`.

## Events

- Native `input` and `change` on the field; `onChange(value)` on the bare `pbClock`. No `pb:` event.

## Do and don't

- Do: use it instead of `type="time"` when the popup must be styled.
