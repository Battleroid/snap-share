DatePicker is a calendar that opens from a date field, for single dates and from/to ranges, used instead of the browser's own date popup, which pages cannot style.

- Consumer provides, single date: `<span class="pb-input pb-datefield">` holding `<input type="text" inputmode="numeric" placeholder="yyyy-mm-dd">` and `<button type="button" class="pb-input__btn" data-cal aria-label="Open calendar">` with `pb-icon--calendar`. Range: `div.pb-daterange` holding two such `.pb-input`s, each with a `from` / `to` `.pb-input__affix`.
- Optional attributes on the field: `data-min`, `data-max` (yyyy-mm-dd; days outside are struck through and disabled), `data-week-start="1"` for Monday, `data-today` to pin today.
- Script: the preview carries the reference script; call `pbDateField(el)` for each field. It builds a `[popover].pb-cal` beside the field, keeps the inputs in yyyy-mm-dd, and fires `change` on them. `pbCalendar({range, from, to, min, max, onChange})` returns a bare calendar for inline use (add `pb-cal--inline`).
- Behavior: typing a date by hand works; Alt+↓ or the calendar button opens it; arrows move by day and week, PageUp/PageDown by month (Shift for year), Home/End to week ends, Enter picks, Esc closes and returns focus. The title switches to a month grid. Ranges show presets (today, 7d, 30d, month), a hover preview and a day count; the calendar closes on the second pick.
- Today has an `accent` ring; picked days fill `accent`; days between fill `accent-tint`.
- Native `type="date"` inputs still work (see InputTypes) where the browser popup is acceptable.
