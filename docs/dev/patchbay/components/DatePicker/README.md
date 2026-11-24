DatePicker is a calendar that opens from a date field, for single dates and from/to ranges, used instead of the browser's own date popup, which pages cannot style.

## Markup

- Consumer provides, single date: `<span class="pb-input pb-datefield">` holding `<input type="text" inputmode="numeric" placeholder="yyyy-mm-dd">` and `<button type="button" class="pb-input__btn" data-cal aria-label="Open calendar">` with `pb-icon--calendar`. Range: `div.pb-daterange` holding two such `.pb-input`s, each with a `from` / `to` `.pb-input__affix`.
- Optional attributes on the field: `data-min`, `data-max` (yyyy-mm-dd; days outside are struck through and disabled), `data-week-start="1"` for Monday, `data-today` to pin today.
- Script: `pbInit()` binds every `.pb-datefield` and `.pb-daterange` (the `datefield` hook); `pbDateField(el)` does the same for one field. It builds a `[popover].pb-cal` beside the field and keeps the inputs in yyyy-mm-dd. `pbCalendar({range, from, to, min, max, onChange})` returns a bare calendar for inline use (add `pb-cal--inline`).
- Today has an `accent` ring; picked days fill `accent`; days between fill `accent-tint`.
- Native `type="date"` inputs still work (see InputTypes) where the browser popup is acceptable.

## States

- Days outside `data-min` / `data-max` are struck through and disabled.
- Ranges show presets (today, 7d, 30d, month), a hover preview and a day count; the calendar closes on the second pick.
- A typed date that cannot be read gets `aria-invalid` and an error line ("Enter a date as yyyy-mm-dd.") joined to its `aria-describedby`, through `pbFieldError`.

## Keys

- Typing a date by hand works. Alt+↓ or the calendar button opens the calendar.
- In the calendar: arrows move by day and week, PageUp/PageDown by month (Shift for year), Home/End to week ends, Enter picks, Esc closes and returns focus. The title switches to a month grid.
- The calendar button has `aria-haspopup="dialog"`.

## Events

- The inputs fire a bubbling native `change` with a yyyy-mm-dd value (a range fires it on both inputs). The component fires no `pb:` events.

## Do and don't

- Do: use it instead of the browser's own date popup, which pages cannot style.
- Do: use the native `type="date"` input where the browser popup is acceptable.
- Don't: ask for a time here; use TimePicker.
