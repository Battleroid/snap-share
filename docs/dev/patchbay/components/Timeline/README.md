Timelines show activity and audit events in order, grouped by day.

## Markup

- `ol.pb-timeline` with `li.pb-timeline__day` headings and `li.pb-event[data-status]` items: `pb-event__mark` (an icon), `p.pb-event__title`, `time.pb-event__time`, optional `pb-event__body`.
- Titles name the actor and the object in bold: "**alex** opened **Rack rebuild**".
- Newest first. Times are local clock times under day headings (today, yesterday, then dates).

## States

- `data-status` colors the mark: `ok`, `warn`, `down`, `info`; none is neutral.

## Keys

Not interactive; it is a plain ordered list, so a screen reader reads it in order.

## Events

None; the component fires no events of its own.

## Do and don't

- Do: use Timeline for the record.
- Don't: use it for unread events aimed at the user; use Notifications.
