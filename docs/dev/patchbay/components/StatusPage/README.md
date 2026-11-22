StatusPage is a full page: header, summary banner, services with 90-day uptime, and an incident log.

- Banner: `.pb-banner` with `data-status="ok|warn|down"` (`pop-mint`, `pop-yellow`, `pop-red`), an icon, one sentence ("One service degraded") and `.pb-banner__meta` for freshness.
- Services: `.pb-services` > `.pb-service` rows: name, host, `.pb-service__right` (uptime % and a Badge), then `.pb-uptime` with one `<i>` per day (`data-s="warn|down|none"`, a `title` with the date and state) and `.pb-uptime__axis`.
- Incidents: `.pb-incident` rows: mono date, title with a Badge (monitoring, resolved), a short serif body saying cause and fix.
- Page title uses `display`; section titles `h2`.
