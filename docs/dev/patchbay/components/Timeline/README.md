Timelines show activity and audit events in order, grouped by day.

- Markup: `ol.pb-timeline` with `li.pb-timeline__day` headings and `li.pb-event[data-status]` items: `pb-event__mark` (an icon), `p.pb-event__title`, `time.pb-event__time`, optional `pb-event__body`.
- Status colors the mark: `ok`, `warn`, `down`, `info`; none is neutral. Titles name the actor and the object in bold: "**alex** opened **Rack rebuild**".
- Newest first. Times are local clock times under day headings (today, yesterday, then dates).
- Use Notifications for unread events aimed at the user; use Timeline for the record.
