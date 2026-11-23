# API

Base path: `/api/v1`. Examples use `$SNAP` for the instance URL and `$KEY` for an API key.

```sh
export SNAP=https://share.example.com
export KEY=snap_…
```

## Authentication

Send an API key as a bearer token:

```sh
curl -H "Authorization: Bearer $KEY" $SNAP/api/v1/items
```

A request with an `Authorization` header uses only that header; cookies are ignored, and a bad key is never retried as a session. Browser sessions can call the same endpoints with their cookie, but only from the same origin.

### API keys

Create keys at **Settings › API keys** (`/settings/keys`). The key is shown once; snap stores only a hash of it. Keys can only be created and revoked from a signed-in browser, never with another key.

A key looks like this (68 characters):

```
snap_aB3dE9xQ0kLm_<43 random characters>_x7Kp2Q
     └ public id ┘ └ secret (256 bits) ┘ └ checksum ┘
```

- The public id (12 characters) identifies the key and is safe to show; the key list displays `snap_<public id>…`.
- The last 6 characters are a CRC-32 checksum of everything before them, so a mistyped or truncated key is refused at once, and secret scanners can recognise snap keys.
- Revoking a key, its expiry passing, or its owner being disabled stops it at the next request.

### Scopes

A key holds the scopes picked when it was created. A browser session holds every scope its account's role allows.

| Scope | Allows |
|---|---|
| `items:read` | `GET /items`, `GET /items/{id}`, tags (reading), `POST /items/bulk` (plus the action's scope), zip downloads |
| `items:write` | uploads, pastes, links, `PATCH /items/{id}`, tags (changing); without `items:read`, only on items the key created (see below) |
| `items:delete` | `DELETE /items/{id}` |
| `albums:read`, `albums:write` | reading and changing albums; without `albums:read`, only albums the key created. Adding items to an album also needs `items:read`. |
| `secrets:read`, `secrets:write` | listing and creating one-time secrets |
| `account:read` | `GET /me` |
| `admin` | admin pages and API; only admins can grant it |

A key's scopes are limited by its owner's current role: if an admin is demoted, their keys lose `admin` immediately and keep the rest.

**Write without read.** A key that cannot read an item (no `items:read`, or no `albums:read` for an album) can change only the items it created itself. For any other item, `PATCH /items/{id}`, `PUT /items/{id}/tags`, `PATCH /albums/{id}` and `POST /albums/{id}/items` answer `404` as for another user's item, so the key cannot list ids, read an item back, make it unlisted, remove its password, rename it to a name it knows or give it an expiry. This is how the ShareX keys (`items:write` only) are limited to their own uploads. Album responses to such a key leave out the members (`items`).

**Public pages.** On share pages, raw URLs and album pages a key counts as the owner only if it holds `items:read`; any other key is a visitor there (private items `404`, password items need the password, no signed raw URLs). `GET /` answers every key as signed out.

Creating anything also needs a verified email address (`403 Verify your email address first.`).

### Key restrictions

Set under **Restrictions** when creating a key:

| Restriction | Effect |
|---|---|
| expiry | the key stops working after a date (default: never) |
| allowed networks | addresses or CIDRs the key may be used from; others get `401 This key is not allowed from this address.` |
| visibility | everything the key creates is `unlisted` or `private`, whatever the request says; `PATCH` and bulk `set_visibility` cannot change it (`403`) |
| max expiry | caps how long anything the key creates lives. Longer expiries and `never` are shortened to the cap; shorter ones are kept. `PATCH` and bulk `set_expiry` are capped too. `expiry_action` `private` is refused (`400`), since the sweep would clear the expiry; when the key changes an item's visibility (`PATCH` or bulk `set_visibility`), or `PATCH`es an item that has no expiry, the cap is applied to the item again and its expiry action set to `delete`. |
| max upload | lowers the size limit for files and pastes the key uploads |

Each key may make 600 requests a minute (bursts of 300); beyond that it gets `429` with `Retry-After`.

## Responses

Success bodies are JSON. Errors are JSON too: `{"error": "Message."}` with a 4xx or 5xx status.

| Status | Meaning |
|---|---|
| `400` | the request is invalid; the message says what to fix |
| `401` | no key, or the key is unknown, wrong, revoked, expired, or used from a network it does not allow. The response carries `WWW-Authenticate: Bearer`. |
| `403` | the key lacks the scope (`This needs the items:write permission.`), the email address is unverified, or the request is the keys API with a key |
| `404` | not found; other users' items, keys and uploads are also `404` |
| `409` | the name is taken, or a resumable upload's offset is wrong |
| `413`, `415`, `507` | upload limits, see [Upload errors](#upload-errors) |
| `429` | too many requests (per key, creations per account, misses on content URLs, unlocks); wait for `Retry-After` seconds |

Unknown, wrong and revoked keys all get the same `401 Invalid credentials.`, so the response never reveals which one it was.

To get only the URL followed by a newline, send `Accept: text/plain` or add `?format=url`:

```sh
curl -H "Authorization: Bearer $KEY" -T photo.png "$SNAP/api/v1/upload/photo.png?format=url"
# https://share.example.com/aB3dE9xQ
```

Created items come back with `201 Created`:

```json
{
  "id": 42,
  "kind": "file",
  "slug": "aB3dE9xQ",
  "url": "https://share.example.com/aB3dE9xQ",
  "page_url": "https://share.example.com/aB3dE9xQ",
  "raw_url": "https://share.example.com/aB3dE9xQ.png",
  "download_url": "https://share.example.com/aB3dE9xQ/download",
  "thumbnail_url": "https://share.example.com/aB3dE9xQ.png",
  "deletion_url": "https://share.example.com/x#Zq3…",
  "name": "photo.png",
  "title": "",
  "size": 48213,
  "type": "image/png",
  "list_type": "image",
  "visibility": "unlisted",
  "password": false,
  "expires_at": "2026-11-29T14:03:00Z",
  "expiry_action": "delete",
  "views": 0,
  "downloads": 0,
  "created_at": "2026-11-22T14:03:00Z"
}
```

- `url` is the share page, or the raw file when the owner turned on "Copy direct link" (unprotected files only).
- `list_type` is the type word lists and the `type` filter use: `image`, `video`, `audio`, `text`, `paste`, `gist`, `link`, `album` or `other`.
- `thumbnail_url` is the raw URL for images until a thumbnail exists.
- `media_state` is `"pending"` on the creation response while the item's thumbnail or poster is still being made (a queued `media.process` job), and absent otherwise.
- `deletion_url` deletes the item without signing in (see [Deletion by token](#deletion-by-token)). It is returned only when the item is created.
- `expires_at` is `null` for items that never expire.

## Options

Uploads, pastes, gists and links take the same options. Multipart uploads send them as form fields, raw-body requests as query parameters, JSON requests as fields.

| Option | Values | Default |
|---|---|---|
| `expires` | `never`, `1h`, `1d`, `7d`, `30d`, `90d`, or an RFC 3339 time | your default |
| `expiry_action` | `delete` or `private` (hide instead of delete) | `delete` |
| `visibility` | `unlisted` (anyone with the link) or `private` (only you) | your default |
| `password` | a password visitors must enter, at least 8 characters | none |
| `naming` | `random`, `words` or `original` | your default |
| `length` | 10–32, for `random` (lowercase letters and digits) | your default (10) |
| `slug` | a custom name: 1–64 of `A-Z a-z 0-9 - _` | — |
| `title` | display title | — |

Raw-body requests never take the password in the query string; send it in the `X-Snap-Password` header.

Names are case-insensitive and unique across files, pastes, gists and links. A taken custom name returns `409`, and so does a name another user freed (deleted, expired or renamed away) less than `items.slug_cooldown_days` ago; the previous owner can take it back. A reserved one (`api`, `login`, `dashboard`…, and well-known root files such as `ads`, `security`, `sitemap`, `google<hex>`; see [uploads.md](uploads.md#names)) returns `400`.

One account creates at most 120 items a minute, bursts of 60 (`429` past that); a gist is one item. Each short link counts 1 KiB toward the quota, and the cached render of a paste, a gist's files or a text file counts with its content.

## Uploads

### Multipart: `POST /api/v1/upload`

The file goes in a field named `file`. Option fields may come before or after it. A `name` field overrides the file name.

```sh
curl -H "Authorization: Bearer $KEY" \
  -F file=@photo.png -F expires=7d -F naming=words \
  $SNAP/api/v1/upload
```

### Raw body: `PUT /api/v1/upload/{filename}`

```sh
curl -H "Authorization: Bearer $KEY" -T backup.tar.zst "$SNAP/api/v1/upload/backup.tar.zst?expires=1d"
curl -H "Authorization: Bearer $KEY" -H "X-Snap-Password: hunter2" -T notes.txt $SNAP/api/v1/upload/
```

(`curl -T file URL/` appends the file name.)

### Resumable: `/api/v1/uploads`

The browser uses this; any client can.

1. Declare the file. Its size is reserved against your quota.

   ```sh
   curl -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
     -d '{"name":"video.mp4","size":734003200,"type":"video/mp4","options":{"expires":"30d"}}' \
     $SNAP/api/v1/uploads
   # {"id":"9f3c…","part_size":8388608,"offset":0,"size":734003200,"expires_at":"…"}
   ```

2. Send parts in order, each with `Content-Length`, at `?offset=` bytes:

   ```sh
   curl -H "Authorization: Bearer $KEY" -X PUT --data-binary @part0 "$SNAP/api/v1/uploads/9f3c…?offset=0"
   # {"offset":8388608,"size":734003200}
   ```

   A wrong offset returns `409 {"error": "…", "offset": N}`; resume from `N`. Parts may be any size up to the remaining bytes.

3. Ask where to resume after a dropped connection:

   ```sh
   curl -I -H "Authorization: Bearer $KEY" $SNAP/api/v1/uploads/9f3c…
   # Upload-Offset: 16777216
   # Upload-Length: 734003200
   ```

   `GET` returns the same as JSON.

4. Finish. The response is the item.

   ```sh
   curl -H "Authorization: Bearer $KEY" -X POST $SNAP/api/v1/uploads/9f3c…/complete
   ```

`DELETE /api/v1/uploads/{id}` abandons an upload and releases its reservation. Uploads idle for 24 hours are removed.

### Upload errors

| Status | Meaning |
|---|---|
| `413` | Over the size limit, or over your quota |
| `415` | File type not allowed, or wrong `Content-Type` for the endpoint |
| `507` | The server is low on disk space |

## Pastes: `POST /api/v1/pastes`

JSON:

```sh
curl -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"content":"package main\n","lang":"go","title":"Example","expires":"1d"}' \
  $SNAP/api/v1/pastes
```

A raw text body, with options in the query string:

```sh
curl -H "Authorization: Bearer $KEY" -H "Content-Type: text/plain" \
  --data-binary @main.go "$SNAP/api/v1/pastes?name=main.go&format=url"
```

A form (`-d`/`--data-urlencode`) uses the field `content`:

```sh
curl -H "Authorization: Bearer $KEY" --data-urlencode content@notes.md -d name=notes.md $SNAP/api/v1/pastes
```

Fields: `content` (required), `lang`, `name`, plus the [options](#options). The language is `lang` if given, else the one the name implies (`main.go` → Go), else plain text. Markdown pastes (`lang: markdown` or a `.md` name) show rendered, with a Source view. Pastes are capped at 16 MB or your upload limit, whichever is lower. Text over 512 KB is shown as a raw link, text over 256 KB is not highlighted, and Markdown over 128 KB shows its source only (see [rendering limits](uploads.md#how-content-is-served)).

## Gists: `POST /api/v1/gists`

Several text files under one link ([uploads.md](uploads.md#gists)). JSON only; other content types get `415`.

```sh
curl -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"title":"Repro","files":[{"name":"main.go","content":"package main\n"},{"lang":"bash","content":"go run .\n"}]}' \
  $SNAP/api/v1/gists
```

Fields: `files` (required, 1–50, in order), `title`, plus the [options](#options). Each file has `content` (required, not blank), `name` and `lang`. Without a name a file is called `file<n>` with its language's extension (`file2.sh`). The language is picked as for pastes. The files together are capped at 16 MB or your upload limit, whichever is lower (`413`). Repeated names (ignoring case) get `400`, blocked extensions `415`. `naming: original` takes the first file's name.

The response is the usual item with `kind: "gist"`, no `raw_url`, `download_url` pointing at a zip of all the files, and `files`:

```json
"files": [
  {"name": "main.go", "lang": "Go", "size": 13, "raw_url": "https://share.example.com/aB3dE9xQ/raw?f=0", "download_url": "https://share.example.com/aB3dE9xQ/download?f=0"},
  {"name": "file2.sh", "lang": "Bash", "size": 9, "raw_url": "https://share.example.com/aB3dE9xQ/raw?f=1", "download_url": "https://share.example.com/aB3dE9xQ/download?f=1"}
]
```

`GET /api/v1/items` and `GET /api/v1/items/{id}` include `files` for gists too. `PATCH /api/v1/items/{id}` changes a gist like any item; its files can't be changed.

## Short links: `POST /api/v1/links`

```sh
curl -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"url":"https://example.com/a/long/path","slug":"docs","expires":"30d"}' \
  $SNAP/api/v1/links

curl -H "Authorization: Bearer $KEY" -d url=https://example.com/ "$SNAP/api/v1/links?format=url"
```

Only `http` and `https` URLs with a host are accepted, up to 2048 characters, without a user name or password. The scheme and host are lowercased and default ports dropped. `GET /{slug}` answers `302` to the target; password-protected links show the password page first. Clicks are counted in `views`.

## Items

### `GET /api/v1/items?limit=50&offset=0`

Your items, newest first. `limit` is 1–200.

```json
{"items": [ … ], "total": 128, "limit": 50, "offset": 0}
```

### List filters

`GET /api/v1/items` and the dashboard take the same query parameters. All are optional and combine (AND). An invalid value answers `400`.

| Parameter | Values |
|---|---|
| `q` | search text, see below |
| `type` | `image`, `video`, `audio`, `text`, `paste`, `gist`, `link`, `album`, `other` |
| `visibility` | `unlisted`, `private`, or `password` (has a password) |
| `expiring` | `1`: expires within 7 days and has not expired yet |
| `album` | an album id: items in that album |
| `tag` | a tag name, ignoring case |
| `from`, `to` | `YYYY-MM-DD` (UTC): created on or after `from`, on or before `to` |
| `opened` | `no` (never opened), `7d` (opened in the last 7 days), `previewed` (only chat or social apps have fetched it) |
| `sort` | `new` (default), `old`, `name`, `size`, `views` (most first), `opened` (last opened first), `expires` (soonest first) |
| `created_before` | RFC 3339 time: created at or before it |

Search covers titles, filenames, the start of text files, pastes and gist files, link targets and tag names. Each word must match (AND) as a substring, ignoring case; operators and quotes are matched as plain text. Words shorter than three characters match titles and filenames only.

```sh
curl -H "Authorization: Bearer $KEY" "$SNAP/api/v1/items?type=image&q=rack&sort=size"
```

### `GET /api/v1/items/{id}`

One item. Other users' items return `404`.

### `PATCH /api/v1/items/{id}`

Any of:

| Field | Effect |
|---|---|
| `title` | set the title (`""` clears) |
| `expires` | as in [options](#options); `"never"` clears |
| `expiry_action` | `delete` or `private` |
| `visibility` | `unlisted` or `private` |
| `password` | a string of at least 8 characters sets it; `null` or `""` removes it |
| `slug` | rename; the old URL stops working, and its name stays reserved for you for the cooldown |

```sh
curl -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" -X PATCH \
  -d '{"expires":"7d","password":null}' $SNAP/api/v1/items/42
```

Changing the password signs out everyone who unlocked the item.

A key without `items:read` gets `404` for every item it did not create (see [Scopes](#scopes)).

### `DELETE /api/v1/items/{id}`

Deletes the item and its files. `204` on success.

```sh
curl -H "Authorization: Bearer $KEY" -X DELETE $SNAP/api/v1/items/42
```

### Tags

Tags are your own labels on items: 1–40 characters, no commas, unique per user ignoring case. An item has at most 20. Search finds items by tag name.

| Request | Scope | Does |
|---|---|---|
| `GET /api/v1/tags` | `items:read` | your tags with item counts: `{"tags": [{"id": 3, "name": "travel", "count": 12}]}` |
| `POST /api/v1/tags` `{"name": "travel"}` | `items:write` | creates a tag (`409` if it exists) |
| `PATCH /api/v1/tags/{id}` `{"name": "trips"}` | `items:write` | renames it |
| `DELETE /api/v1/tags/{id}` | `items:write` | deletes it and takes it off every item |
| `GET /api/v1/items/{id}/tags` | `items:read` | `{"tags": ["beach", "travel"]}` |
| `PUT /api/v1/items/{id}/tags` `{"tags": ["travel", "beach"]}` | `items:write` | replaces the item's tags, creating new names |

## Bulk actions: `POST /api/v1/items/bulk`

One action on many items. Select them by id:

```json
{"ids": [41, 42, 57], "action": "set_expiry", "params": {"expires": "7d"}}
```

or by filter, for "everything matching". `filter` is a query string as in [list filters](#list-filters); `expected` is the count you saw and `created_before` the time you saw it. The server counts again with `created_before` as a bound and answers `409` if the count differs, so nothing you have not seen is changed:

```json
{"filter": "type=image&tag=travel", "expected": 128, "created_before": "2026-11-22T10:00:00Z", "action": "delete"}
```

Every selected item must be yours: one unknown or foreign id fails the request with `404` and changes nothing. At most 1000 ids, or 10000 items by filter. Changes happen in one transaction: all or nothing.

| `action` | `params` | Scope |
|---|---|---|
| `set_expiry` | `expires` (as in [options](#options)), optional `expiry_action` | `items:write` |
| `set_visibility` | `visibility`: `unlisted` or `private` | `items:write` |
| `set_password` | `password` | `items:write` |
| `clear_password` | — | `items:write` |
| `add_tags`, `remove_tags` | `tags`: list of names | `items:write` |
| `add_to_album` | `album_id` | `albums:write` |
| `new_album` | `title`, optional `visibility`, `expires` | `albums:write` |
| `delete` | — | `items:delete` |
| `download_zip` | — | `items:read` |
| `send_email` | `to`: list of addresses | `items:write` |

Every action also needs `items:read`. Unknown `params` fields are refused. Albums in a selection are skipped by `add_to_album` and `new_album` (albums cannot contain albums); the response counts them in `skipped`.

```json
{"action": "set_expiry", "count": 3, "expires_at": "2026-11-29T10:00:00Z"}
```

**`download_zip`** answers with a URL that streams a zip of the files, pastes and gists (stored, not compressed), valid for 10 minutes and only for you. You hold at most 8 such URLs at a time (a new one replaces your oldest); when the server holds 256, new ones get `429`. Pastes are included as text files, and each gist as a folder of its files named after its title (else its slug); links and albums have no content and are skipped. Names come from the filenames, cleaned of path separators and leading dots, with repeats numbered (`photo (2).png`).

```sh
url=$(curl -s -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"ids":[41,42],"action":"download_zip"}' $SNAP/api/v1/items/bulk | jq -r .url)
curl -H "Authorization: Bearer $KEY" -o items.zip "$url"
```

**`send_email`** emails each address the item links with a fixed message, "riley shared a link with you on snap" and each item's kind and link ("image: https://…"). Item titles, file names and link targets are left out, and there is no field for your own text. It needs a verified email address on your account, which becomes the Reply-To. At most 10 addresses and 20 links per send. Each recipient counts once against two daily limits (rolling 24 hours): 20 per user and 200 for the whole server by default. Every send is written to the audit log. Errors: `403` (turned off, or no verified address), `429` (a daily limit), `503` (email is not set up). One-time secrets are not items and are never emailed by the server.

## Albums

An album is an item of kind `album` with an ordered list of your other items. It has its own slug, title, visibility, password and expiry, which apply to the album page only: adding an item never changes it. The album page at `/{slug}` shows each visitor only the members they could open themselves: unlisted ones, and password-protected ones they have already unlocked. Private and expired members are hidden; you see every member, marked.

| Request | Scope | Does |
|---|---|---|
| `GET /api/v1/albums` | `albums:read` | `{"albums": [ … ], "total": 2}`, each with `count` |
| `POST /api/v1/albums` | `albums:write` | creates one: the [options](#options) plus `items` (ids, in order); `201` |
| `GET /api/v1/albums/{id}` | `albums:read` | the album with `count` and `items` (members in order) |
| `PATCH /api/v1/albums/{id}` | `albums:write` | as `PATCH /items/{id}`; answers with the album |
| `DELETE /api/v1/albums/{id}` | `albums:write` | deletes the album; its members stay |
| `POST /api/v1/albums/{id}/items` | `albums:write` | `{"add": [ids], "remove": [ids], "order": [ids]}` |

`add` appends in the given order and skips members already there; `remove` takes items out; `order` moves the listed members to the front in that order and keeps the rest after them. They apply in that order. Members must be your own items and not albums (`404` and `400` otherwise). An album holds at most 2000 items.

```sh
curl -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"title":"Rack rebuild","items":[41,42,43]}' $SNAP/api/v1/albums
```

## oEmbed: `GET /oembed?url=<share page>&format=json`

No authentication. Describes a public item or album for chat apps and other oEmbed consumers ([media.md](media.md#share-pages)): `type` `photo`, `video` (an iframe of `/{slug}/player`) or `link`. Private, password-protected and expired items, and URLs on other hosts, are 404s.

## Deletion by token

Every item gets a deletion URL `https://…/x#<token>` when it is created (ShareX `DeletionURL`). Opening it shows what will be deleted and asks to confirm. The token stays in the URL fragment, so it never reaches access logs.

The page uses two public endpoints, which scripts can call too (rate limited per address):

```sh
curl -H "Content-Type: application/json" -d '{"token":"Zq3…"}' $SNAP/api/v1/delete/lookup
# {"name":"photo.png","kind":"file","type":"png","size":48213,"size_text":"48.2 KB","created_at":"…","url":"…"}

curl -H "Content-Type: application/json" -d '{"token":"Zq3…"}' $SNAP/api/v1/delete
# {"deleted":true}
```

## Account: `GET /api/v1/me`

Who the request acts as, with storage use and limits. Needs `account:read`.

```sh
curl -H "Authorization: Bearer $KEY" $SNAP/api/v1/me
```

```json
{
  "id": 7,
  "username": "ann",
  "role": "user",
  "email_verified": true,
  "quota": {"quota_bytes": 10000000000, "used_bytes": 48213, "reserved_bytes": 0, "max_upload_bytes": 100000000},
  "key": {
    "id": 3,
    "name": "laptop",
    "prefix": "snap_aB3dE9xQ0kLm",
    "scopes": ["items:read", "items:write", "account:read"],
    "expires_at": null,
    "constraints": {"max_upload_bytes": 100000000}
  }
}
```

- `quota_bytes` is `0` when storage is unlimited. `max_upload_bytes` is the largest file you can upload right now, after the site, account and key limits.
- `key` is present only when the request used a key; its `scopes` are those in effect (limited by your role).

## API keys: `/api/v1/keys`

The same as **Settings › API keys**, for scripts running in your browser session (same origin, with the session cookie). Requests made with a key get `403`, so a key can never create, list or revoke keys.

### `GET /api/v1/keys`

```json
{"keys": [{
  "id": 3, "name": "laptop", "public_id": "aB3dE9xQ0kLm", "prefix": "snap_aB3dE9xQ0kLm",
  "scopes": ["items:read", "items:write"], "allowed_cidrs": ["192.168.1.0/24"],
  "constraints": {"visibility": "private", "max_expiry_seconds": 604800, "max_upload_bytes": 100000000},
  "status": "active", "expires_at": null, "last_used_at": "2026-11-22T14:03:00Z", "last_used_ip": "192.168.1.20",
  "created_at": "2026-11-20T09:00:00Z", "revoked_at": null
}]}
```

`status` is `active`, `expired` or `revoked`.

### `POST /api/v1/keys`

```json
{
  "name": "laptop",
  "scopes": ["items:read", "items:write", "items:delete"],
  "expires": "90d",
  "allowed_cidrs": ["192.168.1.0/24", "2001:db8::/32"],
  "constraints": {"visibility": "private", "max_expiry_seconds": 604800, "max_upload_bytes": 100000000}
}
```

| Field | Values |
|---|---|
| `name` | required, up to 64 characters |
| `scopes` | at least one, see [Scopes](#scopes); `admin` only for admins, and not here (`403`) for an admin with two-factor: **Settings › API keys** asks for their password and second factor first |
| `expires` | `never` (default), `7d`, `30d`, `90d`, `1y`, a date `YYYY-MM-DD` (the key works through that day, UTC) or an RFC 3339 time |
| `allowed_cidrs` | addresses or CIDRs, at most 32; empty allows any address |
| `constraints` | optional, see [Key restrictions](#key-restrictions); omitted or `0` means no limit |

The answer is `201` with the key and the token, which is shown only here:

```json
{"key": {"id": 4, "name": "laptop", "prefix": "snap_Q2w3E4r5T6y7", …}, "token": "snap_Q2w3E4r5T6y7_…"}
```

Invalid fields give `400` with `{"error": "…", "fields": {"scopes": "Pick at least one scope."}}`. Each account may hold 50 active keys (`409` beyond that).

### `DELETE /api/v1/keys/{id}`

Revokes the key at once. `204` on success; `404` for keys that are not yours.

## Admin

Admin-only endpoints for scripting, under `/api/v1/admin`. They need an admin's browser session or a key with the `admin` scope, and honour `SNAP_ADMIN_CIDRS`. Everything they change is in the audit log, with the key id when a key was used. See [admin.md](admin.md) for what each action does.

```sh
curl -H "Authorization: Bearer $KEY" "$SNAP/api/v1/admin/users?q=ann"
```

A user looks like this. `quota_bytes` and `max_upload_bytes` are `null` when the site default applies; a quota of `0` means no limit.

```json
{"id": 2, "username": "ann", "email": "ann@example.com", "email_verified": true, "role": "user", "status": "active",
 "sso": false, "items": 12, "secrets": 3, "used_bytes": 48213, "quota_bytes": null, "max_upload_bytes": null,
 "created_at": "2026-11-20T09:12:03Z", "last_login_at": "2026-11-22T08:01:44Z"}
```

| Request | Does |
|---|---|
| `GET /admin/users?q=&limit=50&offset=0` | `{"users": [...], "total": N}`; `q` matches the username or email |
| `GET /admin/users/{id}` | one user |
| `POST /admin/users` | create: `{"username", "email"?, "role"?, "quota_bytes"?, "max_upload_bytes"?}` |
| `PATCH /admin/users/{id}` | change any of `role`, `status` (`active` or `disabled`), `quota_bytes`, `max_upload_bytes` (`null` for the site default) |
| `DELETE /admin/users/{id}` | delete the user with their items and secrets: `{"deleted": true, "items": N, "secrets": N}` |
| `POST /admin/users/{id}/reset` | a new set-password link: `{"emailed": true}`, or `{"emailed": false, "link": "…"}` |
| `POST /admin/secrets/burn` | `{"link": "https://…/s/…"}` or `{"id": "…"}`: `{"id", "state"}` |
| `GET /admin/audit?action=&actor=&from=&to=&limit=&offset=` | `{"entries": [...], "total": N}`, newest first |

Creating a user answers `201` with `{"user": {...}, "emailed": bool}`, plus `"link"` (the set-password link, shown once) when it was not emailed. Invalid fields give `422` naming the field; a taken username or address gives `422` too.

With an API key, links are never returned: they are emailed (the user needs an address and the server SMTP), and creating a user or a reset link that can't be emailed gives `403`, as does giving the `admin` role. Use a browser session or the admin pages for those. Otherwise a key could take a set-password link and sign in as a new admin in a browser, which can do what keys can't.

Demoting, disabling or deleting the last active admin gives `409`. A reset or new-user link is not issued while password sign-in is off (`409` for reset).

For `burn`, send the link without its `#key` part where you can; the server drops it if it arrives, and never logs or stores it. `404` means there is no secret with that id; `422` means the value is neither a secret link nor an id.

Audit filters: `action` is an exact action (`user.delete`) or a prefix ending in a dot (`user.`); `actor` is a username; `from` and `to` are dates (`YYYY-MM-DD`, UTC, both inclusive). An entry:

```json
{"id": 91, "at": "2026-11-22T10:44:53.120Z", "actor": "root", "actor_id": 1, "key_id": 4, "ip": "192.168.1.20",
 "action": "user.limits", "target": "user:2", "details": {"quota_bytes": 10000000, "max_upload_bytes": null}}
```

## Content URLs

| URL | Serves |
|---|---|
| `/{slug}` | share page; short links redirect |
| `/{slug}.{ext}`, `/{slug}/raw` | the content inline (the extension is ignored) |
| `/{slug}/download` | the content as an attachment; for a gist, a zip of its files |
| `/{slug}/raw?f=N`, `/{slug}/download?f=N` | a gist's file N (from 0), inline or as an attachment |
| `/{slug}/thumb` | thumbnail, once one exists |
| `/{slug}/preview` | video hover teaser, once one exists |

Private items open only for their owner (a browser session, or a key with `items:read`); everyone else gets `404`, as for expired and deleted items. Password-protected content answers `403` until the visitor unlocks it on the share page. Misses (`404`) spend a budget of 30 a minute per address, then `429`. With `SNAP_RAW_URL` the raw origin serves only these content URLs and redirects everything else to the app. Content supports `Range` and `If-None-Match`. See [uploads.md](uploads.md) for how content is served safely.
