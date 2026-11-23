Site banners are full-width announcements above the app header.

- Markup: `div.pb-sitebar` with an icon, a `p` (with an optional link) and an optional dismiss button. Default is the accent ground; `data-status="warn|down|ok"` uses the pop colors.
- Roles: `region` with a label for announcements, `alert` for outages, `status` for confirmations.
- One banner at a time. Use it for maintenance, being offline, a full pool, or an update. Problems with one item belong in a Notice on that page.
- Dismissed banners stay dismissed (store the id); outage banners cannot be dismissed.
