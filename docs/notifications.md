# Notifications

snap can tell you when your items are opened and your secrets are revealed, by email and by [ntfy](https://ntfy.sh). Everything is off until you turn it on.

## Settings

**Settings → Notifications** (`/settings/notifications`):

- **Channels.** Two switches that apply at once:
  - **Email** goes to your account's verified address. It needs email set up on the server.
  - **ntfy** publishes to `<server>/<topic>`. Set the server (default `https://ntfy.sh`), a topic (letters, digits, `-` and `_`, up to 64) and, for a protected topic, an access token. The token is stored encrypted and never shown again; the page only says whether one is set. Leave the field empty to keep it, or tick **Remove token**.
- **Item opens.** When to hear about your items: none (the default), the first open, or every open.
- **Send test** sends a test message on one channel right away and shows the result. Up to 5 tests per 10 minutes.

When a delivery fails, the channel shows the time and reason of the last failure (for example `ntfy: server answered 401`).

## What is notified

| Event | When |
|---|---|
| Item, first open | the first time a person opens it, if your item setting is first or every open |
| Item, every open | every open, at most one message per item every 15 minutes |
| Secret revealed | each reveal of a secret created with **notify me** (or switched on later) |
| Secret burned | a secret destroyed by too many wrong passphrases, always, on every channel that is on |

An open is a view, download, click or play by a person, as `docs/stats.md` classifies them. Previews by chat apps, crawlers and your own visits are not opens.

**Every open** is coalesced. The first open sends a message; further opens in the next 15 minutes are counted and sent as one message when the 15 minutes are up ("photo.jpg opened 3 more times · since 14:04 UTC · last …"). That message starts a new 15 minutes.

**Per item.** With a channel on, an item's stats page has a **notify me on** control: default (your setting above), off, first open or every open.

**Per secret.** A secret's own notify flag is its override: set it with **notify me** when you create the secret, or with the switch on the secret's timeline page (`/dashboard/secrets/{id}/timeline`). The item setting above does not apply to secrets. Burns after wrong passphrases are reported whatever the flag says, as a security signal.

## Messages

```
photo.jpg opened · Firefox · macOS · 203.0.113.4 · 14:03 UTC
https://share.example.com/aB3dE9xQ
```

```
Secret "prod db" opened · Firefox · macOS · 203.0.113.4 · 14:03 UTC · 1 of 2 views left
https://share.example.com/dashboard/secrets
```

Item messages link to the item. Secret messages never contain the secret's link, id or key: only your note and a link to your secrets page. Times are UTC, with the date when it is not today.

- **Email:** the line above as the subject, plain text and simple HTML bodies, and a link to the notification settings.
- **ntfy:** `POST <server>/<topic>` with the detail as a plain-text body and the headers `Title` (RFC 2047 encoded when not ASCII), `Tags` (`snap` and `open`, `reveal`, `burn` or `test`), `Click` (the link) and `Priority: 4` for burns. A token is sent as `Authorization: Bearer <token>`.

## Limits and delivery

- At most 30 messages per user per hour; beyond that, messages are dropped (and logged). Burn messages are not limited.
- Delivery runs as background jobs: `notify.open` decides and renders an item message a few seconds after the open, `notify.send` delivers one message to one channel. A failed send is retried with backoff, up to 5 attempts. A 4xx answer (other than 429), a refused address, a redirect or missing setup fails at once. A channel turned off before its message goes out is skipped.
- Errors in logs, in the jobs table and on the settings page never contain the server URL, topic or token.
- A queued message holds what it reports (the item name, the visitor's IP and browser) in the jobs table until finished jobs are deleted after 7 days.

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
| `notify_prefs` | per user: the two channel switches, ntfy server and topic, the sealed token, the default for item opens |
| `notify_overrides` | per-item overrides, with the owner who set them (ignored and swept if the item changes hands or is deleted) |
| `notify_windows` | the 15-minute windows and held-back counts for every-open coalescing |
| `notify_status` | the last success and failure per user and channel |

The ntfy token is sealed with AES-256-GCM under a key derived from `SNAP_SECRET_KEY` (HKDF-SHA256, info `snap notify ntfy token v1`), with a random nonce and the user id as associated data. Tokens sealed under `SNAP_SECRET_KEY_PREVIOUS` still open during a key rotation; once the previous key is gone, users have to set their token again.
