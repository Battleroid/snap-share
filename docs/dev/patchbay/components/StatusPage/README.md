StatusPage is a full page: header, summary banner, services with 90-day uptime, and an incident log.

## Markup

- Banner: `.pb-statusbanner` with an icon, one sentence ("One service degraded") and `.pb-statusbanner__meta` for freshness.
- Services: `.pb-services` > `.pb-service` rows: name, host, `.pb-service__right` (uptime % and a Badge), then `.pb-uptime` with one `<i>` per day (`--days` sets the count, default 90) and `.pb-uptime__axis`.
- Incidents: `.pb-incident` rows: mono date, title with a Badge (monitoring, resolved), a short serif body saying cause and fix.
- Page title uses `display`; section titles `h2`.

## States

- Banner `data-status="ok|warn|down"` (`pop-mint`, `pop-yellow`, `pop-red`).
- Uptime day `<i>`: no attribute is an up day; `data-s="warn|down|none"` marks the others, with a `title` carrying the date and state.

## Keys

Not interactive. Say the overall state in the banner text, not by color alone.

## Events

None; the component fires no events of its own.

## Do and don't

- Do: put the cause and the fix in the incident body.
