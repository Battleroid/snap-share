The event calendar shows scheduled work by day: backups, scrubs, maintenance windows.

- Markup: `.pb-evcal[data-evcal][data-view=month|agenda][data-max]` with `pb-evcal__head` (a `pb-btngroup` of `[data-evcal-nav=prev|today|next]`, `h3.pb-evcal__title`, a `[data-evcal-view]` radiogroup), an empty `pb-evcal__body`, `pb-evcal__legend` and a `<script type="application/json">` of `{month, today, kinds: {key: {label, c}}, events: [{date, time, title, kind, status, repeat}]}`.
- `repeat` is `daily`, `weekly:N` (0 = Sunday) or `monthly:D`. A dated event with the same title and time replaces that day's repeat, so a failed run (`status: "down"`) shows once, marked failed.
- `pbEventCal(el)` draws the month grid (today highlighted, `data-max` events per day then "+N more", which opens the agenda) or the agenda list. It fires `select` with the date and event. Under 620px events become colored dots.
- Colors come from `kinds`; keep it to four. Failed runs always say "failed".
