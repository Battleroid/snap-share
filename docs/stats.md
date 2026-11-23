# Stats

snap records requests to shared items and secrets so the owner can see whether a link was opened, and by what. Stats are shown only to the owner; admins see totals only. The owner's own requests are never counted.

## What is recorded

Item events:

| Kind | When |
|---|---|
| `view` | a browser loads the share page |
| `embed` | raw media is loaded from another site, or the video player page is framed by one |
| `download` | the raw file is downloaded |
| `click` | a short link is followed |
| `play` | audio or video is played |
| `preview` | a chat or social app fetches the link to build a preview |

Secret events: `link_opened` (the secret link was loaded), `revealed`, `failed` (wrong passphrase) and `burned`. Anonymous secrets record nothing.

Each event stores the time, client IP, User-Agent, a short summary of it (`Firefox · macOS`, `curl`, `Discord bot`), the preview bot's name, the referring site's host name (never the path or query) and a visitor key.

Per item and day, snap also keeps totals: views, unique visitors, new visitors, previews, embeds, downloads, clicks and plays.

## How requests are classified

The rules are in `internal/stats/bots.txt`, matched against the User-Agent, first match wins:

- **Preview bots** (Discord, Slack, Telegram, Twitter, Facebook, WhatsApp, iMessage, LinkedIn, Mattermost, Teams, Pinterest, Reddit, Mastodon, Bluesky, Matrix, Iframely, Embedly and others) are recorded as `preview` with the bot's name, never as views. A link that only bots have fetched shows as "previewed", not "opened".
- **Requests with no User-Agent** are recorded as previews named `Unknown`. Signal and some other apps fetch previews this way.
- **Dropped, not recorded:** search engines, SEO and AI crawlers, uptime monitors, scanners, headless browsers, anything calling itself a crawler or spider or carrying a `+http` info URL, `HEAD` requests and browser prefetches.
- **Everything else is a person**, including curl, Wget, HTTPie, scripts and the `sp` CLI. Unknown User-Agents count as people.

Repeats are merged: the same visitor, kind and item within 30 minutes of the previous request count once. A video's many Range requests make one `play`, and reloading a page is not a new view.

## Unique visitors and "opened"

A visitor is identified by a key derived from the item, the IP address and the User-Agent, keyed with a server secret, so the same person has unrelated keys on different items. IPv6 addresses are grouped by their /64.

Views, downloads, clicks and plays mark an item as opened and count towards unique visitors. Embeds and previews do not: they are often fetched by caches, CDNs and bots.

**LAN visitors.** Behind the home router, every visitor on the LAN reaches snap from `192.168.1.1` (hairpin NAT). LAN visitors are therefore told apart only by their browser: two people on the same browser and OS count as one unique visitor, and their events all show that IP.

## Retention

Raw events, which hold IPs and User-Agents, are deleted after the retention period (admin setting, 90 days by default). Visitor keys are deleted on the same schedule. Daily totals are kept for as long as the item exists.

As a result:

- Unique visitors for a date range are counted from raw events, so they only reach back as far as retention. The all-time unique count is kept in the daily totals.
- A visitor who returns after their key was deleted counts as new.

## What each request to an item records

`internal/items` records one event per request that reaches a share page, short link or item content, after the access checks. The owner's own signed-in requests, and requests the rules above drop, record nothing.

| Request | Event |
|---|---|
| a preview bot fetches the page, raw file, download or thumbnail | `preview`, with the bot's name |
| the share page as a document (`Sec-Fetch-Dest: document`), or by a client without fetch metadata (curl) | `view` |
| a short link | `click` |
| `/download`, or the raw file opened as a document (a new tab) | `download` |
| the raw file as a subresource (image, video, audio, iframe, fetch) of a page that is not snap's | `embed`, with that site's host |
| a raw file with a foreign `Referer` and no fetch metadata | `embed` |
| the video player page (`/{slug}/player`), loaded by a person | `embed` |
| raw audio or video played from the share page, or Range requests without fetch metadata | `play` |
| anything else without fetch metadata (curl downloading the raw file) | `download` |
| the share page's own images, thumbnails, hover teasers | nothing |

"Not snap's" means the `Referer` host is neither the public nor the raw host and the request is not same-origin. Snap's own hosts are never stored as the referring site.

Repeats of one kind from one visitor within 30 minutes are merged (`stats.Deduper`), so a reload is one view and a video's Range requests are one play. Share pages load audio, and video that has a poster, only when played (`preload="none"`), so a play means someone pressed play. A video without a poster (no ffmpeg, or its job has not run yet) loads its metadata with the page, which records a play.

These events are separate from the items' own `views`/`downloads`/`opened_count` counters.

**Raw origin.** With `SNAP_RAW_URL` set, raw files and thumbnails are served from the raw origin, where the owner's session cookie does not reach. The owner's own raw requests there count like anyone else's.

**Previewed.** `items.previewed_at` holds the first time a preview bot fetched the item. It is set by the items counter flush (every 10 s). The `item.opened` badge partial shows `previewed` when it is set and `first_opened_at` is not; the API's item JSON has `previewed_at`.

## Retention setting

| Key | Default | Notes |
|---|---|---|
| `stats.retention_days` | `90` | 1–3650. Raw item and secret events, and visitor keys, older than this are deleted once a day; in-app notifications (`docs/notifications.md`) every hour. |

The visitor-key secret is derived from `SNAP_SECRET_KEY` (HKDF-SHA256, info `snap stats visitor key`), shared by item and secret events. Rotating `SNAP_SECRET_KEY` makes every returning visitor count as new.

## Stats page

`/dashboard/items/{id}/stats` is the owner's view of one item (the dashboard row menu links here):

- the opened badge, and the first and last open;
- tiles for the chosen window (7, 30 or 90 days): views, unique visitors, previews, and what applies to the kind (downloads, plays and embeds for files, clicks for short links), each with its daily trend;
- views per day as a chart;
- the top referring sites, preview bots and browsers/OS in the window;
- the latest raw events, 50 per page, with time, kind, IP, client, referring site and bot, and **Export CSV** (`/dashboard/items/{id}/events.csv`: every event still within retention, newest first).

Cells in the CSV that start with `=`, `+`, `-`, `@`, a tab or a carriage return get a leading `'`, so a crafted User-Agent can't run as a spreadsheet formula.

Only the owner sees visitor details. An admin opening another user's item gets the tiles and the chart (totals only, without the item's name or link), and only from `SNAP_ADMIN_CIDRS` when that is set; everyone else gets a 404, as for a missing item. The pages need a browser session.

When the owner has a notification channel on, the page also shows the item's notification override (see `docs/notifications.md`).

## Secret timeline

`/dashboard/secrets/{id}/timeline` lists every event of one of your secrets, newest first, 20 to a page (on the dashboard's Secrets tab it opens in a dialog): link opened, revealed (with "1 of 2"), wrong passphrase (with "2 of 10") and burned (with the reason), each with time, IP and client. With a notification channel on, it has a switch for the secret's reveal notifications. Owner only; anyone else gets a 404.

## API

Both endpoints need the `items:read` scope and answer only for your own items (404 otherwise, also for admins).

### `GET /api/v1/items/{id}/stats?from=&to=`

`from` and `to` are dates (`2026-11-01`, UTC) or RFC 3339 times. A date as `to` includes that day. The default is the last 30 days up to now; the range is at most 1000 days.

```json
{
  "item": {"id": 7, "slug": "aB3dE9xQ", "name": "photo.jpg", "kind": "file", "url": "https://share.example.com/aB3dE9xQ"},
  "from": "2026-10-24T00:00:00Z", "to": "2026-11-23T00:00:00Z",
  "opened": {"first_opened_at": "…", "last_opened_at": "…", "opened_count": 4, "previewed_at": null},
  "totals": {"views": 12, "uniques": 5, "previews": 3, "embeds": 0, "downloads": 2, "clicks": 0, "plays": 0},
  "daily": [{"day": "2026-10-24", "views": 0, "uniques": 0, "new_visitors": 0, "previews": 0, "embeds": 0, "downloads": 0, "clicks": 0, "plays": 0}],
  "referrers": [{"key": "news.example", "count": 3}],
  "bots": [{"key": "Discord", "count": 2}],
  "browsers": [{"key": "Firefox · macOS", "count": 4}]
}
```

Totals come from the daily rollups and cover every UTC day the range touches. `uniques` for a range counts distinct visitors in the raw events, so it reaches back only as far as retention. The top lists hold up to 10 entries.

### `GET /api/v1/items/{id}/events?before=&limit=`

Raw events, newest first, with IP and User-Agent. `limit` is 1–1000 (default 100). `next_before` is the cursor for the next page, `null` at the end.

```json
{"events": [{"id": 912, "at": "2026-11-22T14:03:11Z", "kind": "view", "ip": "203.0.113.4",
  "user_agent": "Mozilla/5.0 …", "ua_summary": "Firefox · macOS", "bot": "", "referrer": "news.example"}],
 "next_before": 911}
```

## CLI

```
sp stats <slug|url|id> [--days 30] [--events] [--limit 20]
```

Prints the opened state, totals for the last `--days` days, views per day as a text sparkline, and the top referrers, bots and browsers. `--events` adds the latest raw events with IPs. The key needs `items:read`.
