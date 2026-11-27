# Notifications

snap tells you about your items, secrets and storage: in the app (the bell), by email and by [ntfy](https://ntfy.sh). **Settings → Notifications** has a grid of events × channels that picks what goes where.

## Events

| Event | When | Bell | Email, ntfy |
|---|---|---|---|
| `secret.burned_failures` | a secret is destroyed by too many wrong passphrases; every time, not capped | on | on |
| `secret.wrong_passphrase` | a wrong passphrase that does not destroy it; at most one message per secret every 15 minutes, then a summary | on | off |
| `secret.revoked_by_admin` | an admin removes one of your secrets (Admin › Secrets) | on | off |
| `secret.revealed` | each reveal; by email and ntfy only for secrets with **notify me** | on | on |
| `secret.expired_unopened` | a secret expires without ever being opened; by email and ntfy only with **notify me** | on | off |
| `item.first_open` | the first open of an item by each person | on | on if the old item-open setting was first or every open |
| `item.open` | every open, at most one message per item every 15 minutes, then a summary | off | on if the old setting was every open |
| `item.expiring` | an item's expiry is less than a day away; once per item and expiry date (checked hourly) | on | off |
| `item.expired` | the expiry sweep deleted the item or made it private | on | off |
| `quota.95` | an upload takes your storage past 95% of your quota | on | off |
| `quota.80` | an upload takes your storage past 80% | on | off |

The defaults are what snap did before the grid: the bell lists first opens, reveals and burns, and email and ntfy get burns, reveals of secrets with **notify me**, and item opens as the old **Item opens** setting said. The new events go to the bell only until you tick them.

An open is a view, download, click or play by a person, as `docs/stats.md` classifies them; previews, crawlers and your own visits are not. Your own reveals and wrong passphrases, and anonymous secrets, are not reported.

**Storage** fires once per crossing: after a level has fired, it fires again only once an upload has found usage below it. A jump past both levels sends only `quota.95`. Users without a quota get nothing.

**Coalescing.** `item.open` and `secret.wrong_passphrase` send the first event at once; further ones in the next 15 minutes are counted and sent as one message when the 15 minutes are up ("photo.jpg opened 3 more times · since 14:04 UTC · last …", `Secret "prod db": 2 more wrong passphrases`). That message starts a new 15 minutes. A burn ends the secret's window.

## Overrides

The grid decides the channels; an item's or a secret's own setting still decides whether it is reported by email and ntfy at all. The bell follows the grid alone.

- **Per item.** With a channel on, an item's page has a **notify me on** control: default (the grid), off, first open or every open. Off also keeps the item's expiry events off email and ntfy. First or every open sends that item's opens to the channels ticked for `item.first_open` or `item.open`, or, when neither is ticked for any channel, to every channel that is on.
- **Per secret.** A secret's notify flag (**notify me** when you create it, or the switch on its timeline page, `/dashboard/secrets/{id}/timeline`) gates `secret.revealed` and `secret.expired_unopened` on email and ntfy. Burns, wrong passphrases and an admin's removal follow the grid whatever the flag.

## In the app

The bell in the header shows how many notifications you haven't read; it opens the latest 20. **All notifications** (`/notifications`) lists them all, newest first, 50 to a page. Rows are `info`, `warn` (wrong passphrase, expiring item, removal, storage past 80%) or `down` (burn, storage past 95%), and link to the item's page, the secret's timeline or, for a deleted item and for storage, the dashboard.

Clicking a row marks it read and opens its link; **Mark all read** clears the count. The × on a row deletes it (`POST /notifications/{id}/delete`), and **Clear read** on `/notifications` deletes every read one (`POST /notifications/clear-read`); both are plain forms that also work without JavaScript, and only ever touch your own rows (anyone else's id is a `404`). The count and an open list update live while a page is open, over Server-Sent Events (`GET /notifications/stream`): the unread count on connect, then an event per new notification, per read and per deletion (so other tabs follow), and a comment every 25 seconds. Each stream ends after 5 minutes and the browser reconnects, so a signed-out session stops getting updates within minutes. A user can have 4 streams open and the server 500; more get `429` (streams from addresses in `SNAP_RATELIMIT_EXEMPT` count only towards the 500). A tab hidden for a minute closes its stream and reconnects when it is shown. The routes need a browser session (API keys get `403`) and are not served on the raw origin.

Rows hold the visitor's IP and browser, so they are kept as long as raw stats events (`stats.retention_days`, 90 days by default; swept every hour), at most 200 per user (older ones are deleted). Read rows go sooner: `notify.read_retention_hours` after they were read (24 by default; `0` keeps them for the full retention), and the page says so. Item rows go with their item (within the hour), secret rows with their secret when its tombstone is purged, and account rows (storage, a deleted item) stay until retention.

## Settings

**Settings → Notifications** (`/settings/notifications`):

- **Channels.** Two switches that apply at once:
  - **Email** goes to your account's verified address. It needs email set up on the server. If SMTP is removed while the channel is on, nothing is sent or logged and the channel shows `email: not set up on this server`.
  - **ntfy** publishes to `<server>/<topic>`. Set the server (default `https://ntfy.sh`), a topic (letters, digits, `-` and `_`, up to 64) and, for a protected topic, an access token. The token is stored encrypted and never shown again; the page only says whether one is set. Leave the field empty to keep it, or tick **Remove token**. Changing the server removes a saved token, so it never goes to a server it wasn't given for. On a public server such as ntfy.sh, anyone who guesses a topic can read it: use a long random topic, an access token or your own server.
- **Events.** The grid: one row per event, one column per channel (bell, email, ntfy), and an **All events** row that ticks a whole column. A channel's column is disabled while its switch is off, and saving leaves a disabled column's choices as they are. **Save** posts the grid to `POST /settings/notifications/events` (a field `channel` per column saved, and `on` as `event:channel` per ticked box; a JSON answer with `Accept: application/json`).
- **Test**, under the email and ntfy columns, sends a test message on that channel right away and shows the result. Up to 5 tests per 10 minutes.

When a delivery fails, the channel shows the time and reason of the last failure (for example `ntfy: server answered 401`).

## Messages

```
photo.jpg opened · Firefox · macOS · 203.0.113.4 · 14:03 UTC
https://share.example.com/aB3dE9xQ
```

```
Secret "prod db" opened · Firefox · macOS · 203.0.113.4 · 14:03 UTC · 1 of 2 views left
https://share.example.com/dashboard/secrets
```

Item messages link to the item (the dashboard once it is deleted) and storage messages to the dashboard. Secret messages never contain the secret's link, id or key: only your note and a link to your secrets page. Times are UTC, with the date when it is not today.

- **Email:** the line above as the subject, plain text and simple HTML bodies, and a link to the notification settings.
- **ntfy:** `POST <server>/<topic>` with the detail as a plain-text body and the headers `Title` (RFC 2047 encoded when not ASCII), `Tags` (`snap` and one of `open`, `reveal`, `burn`, `warning`, `removed`, `expired`, `expiring`, `quota` or `test`), `Click` (the link) and `Priority: 4` for burns. A token is sent as `Authorization: Bearer <token>`.

## Limits and delivery

- At most 30 item messages (opens, expiring, expired) and 30 secret messages (reveals, wrong passphrases, expiries, removals) by email and ntfy per user per hour, counted separately so a busy item can't crowd out secrets; beyond that, messages are dropped (and logged). Burns and storage messages are not limited. The bell has no hourly limit.
- Delivery runs as background jobs: `notify.open` decides and renders an item message a few seconds after the open, `notify.secret` sends a wrong-passphrase summary, `notify.send` delivers one message to one channel. `notify.expiring` looks for expiring items every hour. A failed send is retried with backoff, up to 5 attempts. A 4xx answer (other than 429), a refused address, a redirect or missing setup fails at once. A channel turned off before its message goes out is skipped.
- Errors in logs, in the jobs table and on the settings page never contain the server URL, topic or token.
- A queued message holds what it reports (the item name or the secret's note, the visitor's IP and browser) in the jobs table until it is delivered or gives up. Finished `notify.open`, `notify.secret` and `notify.send` jobs are deleted within 10 minutes (other jobs are kept 7 days); the result stays in `notify_status`.
- Without SMTP, email messages are not queued at all.

## Outbound requests

ntfy requests go through one HTTP client that won't reach the server's own network:

- https only, unless the admin allows plain http;
- every address the host resolves to is checked when it is dialled, which also stops DNS rebinding;
- loopback, private (10/8, 172.16/12, 192.168/16, fc00::/7), link-local, CGNAT (100.64/10), multicast, unspecified, reserved and documentation ranges and the cloud metadata addresses (169.254.169.254, fd00:ec2::254) are refused, also as IPv4-mapped, IPv4-compatible, NAT64 or 6to4 IPv6 addresses;
- no redirects; proxy variables in the environment are ignored; 5 s to connect, 10 s in total; at most 64 KB of the answer is read.

Admin settings (`snap-server admin settings set <key> <json>`):

| Key | Default | Notes |
|---|---|---|
| `notify.allow_http` | `false` | allow `http://` ntfy servers |
| `notify.outbound_allow` | `[]` | CIDRs, IP addresses and exact host names that may be reached although they are internal, e.g. `["ntfy"]` for an ntfy container on the same Docker network, or `["10.0.20.0/24"]` |

A host name entry allows whatever that name resolves to; a CIDR allows those addresses under any name.

Example for a self-hosted ntfy next to snap:

```sh
snap-server admin settings set notify.outbound_allow '["ntfy"]'
snap-server admin settings set notify.allow_http true   # if it listens on plain http
```

Then set the server to `http://ntfy` (or `http://ntfy:8080`) in your notification settings.

## Storage

| Table | Holds |
|---|---|
| `notify_prefs` | per user: the two channel switches, ntfy server and topic, the sealed token, and the old item-open setting (`default_event`, the default of the item-open rows not yet saved) |
| `notify_event_prefs` | the events grid as saved: one row per user, event and channel (`inbox`, `email`, `ntfy`); a missing row takes the default |
| `notify_marks` | once-per-subject events: the expiry date an `item.expiring` went out for, and each user's storage level (0, 80 or 95) |
| `notify_overrides` | per-item overrides, with the owner who set them (ignored and swept if the item changes hands or is deleted) |
| `notify_windows` | the 15-minute windows and held-back counts for every-open and wrong-passphrase coalescing (`item` or `secret`; a secret's go with it) |
| `notify_status` | the last success and failure per user and channel |
| `inbox` | the in-app notifications: kind, status, title, the visitor's browser and IP, link, subject (`item`, `secret` or `account`), time and read time (see In the app) |

The ntfy token is sealed with AES-256-GCM under a key derived from `SNAP_SECRET_KEY` (HKDF-SHA256, info `snap notify ntfy token v1`), with a random nonce and the user id as associated data. Tokens sealed under `SNAP_SECRET_KEY_PREVIOUS` still open during a key rotation; once the previous key is gone, users have to set their token again.
