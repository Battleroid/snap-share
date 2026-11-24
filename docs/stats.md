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

**Previewed.** `items.previewed_at` holds the first time a preview bot fetched the item. It is set by the items counter flush (every 10 s). While `first_opened_at` is unset, the item's stats page shows it in its meta line ("previewed by a chat app") and the dashboard's Opened › previewed filter lists the item; the API's item JSON has `previewed_at`.

## Retention setting

| Key | Default | Notes |
|---|---|---|
| `stats.retention_days` | `90` | 1–3650. Raw item and secret events, and visitor keys, older than this are deleted once a day; in-app notifications (`docs/notifications.md`) every hour. |

The visitor-key secret is derived from `SNAP_SECRET_KEY` (HKDF-SHA256, info `snap stats visitor key`), shared by item and secret events. Rotating `SNAP_SECRET_KEY` makes every returning visitor count as new.

## Item page

`/dashboard/items/{id}` is the owner's page for one item. These link to it:

- **Manage** on the share page;
- **Details** in the dashboard row menu;
- notifications;
- the Top items on Stats.

The old `/dashboard/items/{id}/stats` redirects to it. It has:

- the kind, the link name and the first and last open in the meta line (plus when it was previewed, while only a chat app's preview bot has fetched it), with a breadcrumb back to Items;
- **Open item** and **Edit**, which opens the same drawer as the dashboard's Edit (title, link name, expiry, visibility, password, tags);
- the preview beside the details:
  - the link;
  - the type and size, with dimensions and length for media, or the target for a short link;
  - visibility and password;
  - when it was uploaded, when it expires and what happens then;
  - its tags and the albums it is in;
  - the sha256;
- **Copy link**, the QR code, **Download** (files, pastes and gists) and **Delete…**, which asks first;
- tiles for the chosen window (7, 30 or 90 days): views, unique visitors, previews, and what applies to the kind (downloads, plays and embeds for files, clicks for short links), each with its daily trend;
- views and visitors per day as a chart (hover a day for its date and counts, drag to zoom);
- the top referring sites, preview bots and browsers/OS in the window;
- the latest raw events, 50 per page, with time, kind, IP, client, referring site and bot, and **Export CSV** (`/dashboard/items/{id}/events.csv`: every event still within retention, newest first).

Cells in the CSV that start with `=`, `+`, `-`, `@`, a tab or a carriage return get a leading `'`, so a crafted User-Agent can't run as a spreadsheet formula.

Only the owner sees visitor details. An admin opening another user's item gets the tiles and the chart (totals only, without the item's name or link), and only from `SNAP_ADMIN_CIDRS` when that is set; everyone else gets a 404, as for a missing item. The pages need a browser session.

When the owner has a notification channel on, the page also shows the item's notification override (see `docs/notifications.md`).

## Your stats

`/dashboard/stats` (the sidebar's **Stats**) adds up everything for your own items and secrets, over the same ranges as an item's page (7, 30 or 90 days, 30 by default). Every number comes from what snap already records, for items you own only:

- tiles for views, downloads, plays, clicks, previews and embeds, summed over your items from the daily totals, each with its daily trend. There is no unique-visitor total: a visitor key belongs to one item, so visitors can't be added up across items (an item's own page shows them);
- views per day as a chart (hover a day for its date and count, drag to zoom);
- **Top items**: up to 10 items ranked by views in the range, with downloads and plays/clicks, each linking to its stats page;
- **Top referrers** and **Clients** from the raw events of all your items, previews left out. Raw events are deleted after the retention period, so a 90-day range can reach back further than these two lists;
- **Secrets**: how often your secret links were opened, revealed and failed (wrong passphrase) in the range, from the raw secret events, with a line such as "7 of 9 opened links were revealed". It shows only when you have secrets;
- **Storage**: used space, as a bar split into images, video, audio and other (with a quota, measured against it, with the free part shown), and **Items by type**, most first, kinds with none left out.

Your own visits are not counted, here or on an item's page. An account with no items and no secrets sees an empty state. Admins see their own numbers like everyone else; the page is not a way to see other users' items. It needs a browser session.

The Usage section of Settings > Profile shows the same storage bar and items-by-type panels, and its tiles use the same queries (`internal/stats`: `UserTotals`, `UserDaily`, `UserSecretDaily`, `UserItemsCreatedDaily`, `UserBytesAddedDaily`, `UserItemCounts`, `UserStorageKinds`). They count items that still exist; a deleted item takes its bytes out of the past.

## Secret timeline

`/dashboard/secrets/{id}/timeline` lists every event of one of your secrets, newest first, 20 to a page (on the Secrets page it opens in a dialog): link opened, revealed (with "1 of 2"), wrong passphrase (with "2 of 10") and burned (with the reason), each with time, IP and client. With a notification channel on, it has a switch for the secret's reveal notifications. With more than one visitor, a **Viewers** strip has one chip per distinct visitor ("Visitor 1", "Visitor 2" in the order they first appeared, or the preview bot's name) with their IP, client and counts of opened, revealed and failed. A chip filters the timeline to that visitor; "All" shows everyone again. The events sit under day headings and name their visitor. Visitors are told apart by the stored visitor key, but the key itself is never shown. Events older than the retention period are deleted; the end of the timeline says so.

Owner only; anyone else gets a 404.

## API

These endpoints need the `items:read` scope and answer only for your own items (404 otherwise, also for admins).

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

### `GET /api/v1/stats?days=`

Your own totals across all your items, as on the Your stats page. It needs `items:read`; the `secrets` object is only there when the key also has `secrets:read` (a session has both), and is `null` otherwise or when you have no secrets. `days` is 7, 30 (default) or 90.

```json
{
  "from": "2026-10-24T00:00:00Z", "to": "2026-11-23T00:00:00Z", "days": 30,
  "totals": {"views": 462, "downloads": 38, "plays": 71, "clicks": 31, "previews": 27, "embeds": 15},
  "daily": [{"day": "2026-10-24", "views": 8, "downloads": 0, "plays": 1, "clicks": 0, "previews": 0, "embeds": 0}],
  "top_items": [{"id": 7, "slug": "aB3dE9xQ", "name": "photo.jpg", "kind": "image", "views": 40, "downloads": 4, "plays_clicks": 0}],
  "referrers": [{"key": "news.example", "count": 3}],
  "clients": [{"key": "Firefox · macOS", "count": 4}],
  "secrets": {"opened": 9, "revealed": 7, "failed": 1},
  "storage": {"used": 1200000000, "quota": 10000000000, "images": 420000000, "video": 510000000, "audio": 60000000, "other": 210000000},
  "items_by_type": [{"key": "image", "count": 38}]
}
```

`quota` is 0 without a quota. `images`, `video` and `audio` are the bytes of those files; `other` is the rest of `used` (text and other files, pastes, links, secrets), so the four add up to `used`. `items_by_type` keys are `image`, `video`, `audio`, `text`, `other`, `paste`, `gist`, `link` and `album`, most first, kinds with none left out. Referrers and clients hold up to 10 entries and come from raw events, which only reach back as far as retention.

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
