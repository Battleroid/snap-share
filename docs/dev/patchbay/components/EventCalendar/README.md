The event calendar shows scheduled work by day: backups, scrubs, maintenance windows.

## Markup

- `.pb-evcal[data-evcal][data-view=month|agenda][data-max]` with `pb-evcal__head` (a `pb-btngroup` of `[data-evcal-nav=prev|today|next]`, `h3.pb-evcal__title`, a `[data-evcal-view]` radiogroup), an empty `pb-evcal__body`, `pb-evcal__legend` and a `<script type="application/json">` of `{month, today, kinds: {key: {label, c}}, events: [{date, time, title, kind, status, repeat}]}`.
- `repeat` is `daily`, `weekly:N` (0 = Sunday) or `monthly:D`. A dated event with the same title and time replaces that day's repeat, so a failed run (`status: "down"`) shows once, marked failed.
- `pbEventCal(el)` (the `evcal` hook) draws the month grid (today highlighted, `data-max` events per day then "+N more", which opens the agenda) or the agenda list. `el.pbGo("YYYY-MM")` jumps to a month.
- Colors come from `kinds` (`c` is a CSS color or token); keep it to four.

## States

- Month or agenda view. A failed run (`status: "down"`) is red with an outline and the word "failed" in the month chip and in the agenda.
- Under 620 px of the calendar's own width the month cell is a 48 px target and events show as small marks (a failed one is red with a ring) instead of chips; tapping a day opens the agenda. In that layout the failure is carried by the day's name ("1 failed") and the agenda, not by the mark alone.

## Keys

- The month is an ARIA date grid with one tab stop (the focused day). Arrows move by day and week, Home and End go to the week's ends, PageUp and PageDown change month (Shift: year). Enter or Space on a day with events opens the agenda at that day.
- Event chips are for the pointer in the month and real buttons in the agenda. Each day is named like "Friday, November 13, 1 event, 1 failed" (counts follow the locale's plural rules).

## Events

- `pb:select` with `detail: {date, event}` when an event chip is clicked.
- `pb:day` with `detail: {date, events}` when a day is opened (Enter, Space, click or "+N more").

## Do and don't

- Do: keep the number of kinds to four.
- Do: let failed runs always say "failed" in the chip and the agenda.
- Don't: show a failure by color alone.
