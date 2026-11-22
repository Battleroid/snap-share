# Stats

snap records requests to shared items and secrets so the owner can see whether a link was opened, and by what. Stats are shown only to the owner; admins see totals only. The owner's own requests are never counted.

## What is recorded

Item events:

| Kind | When |
|---|---|
| `view` | a browser loads the share page |
| `embed` | raw media is loaded from another site |
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
