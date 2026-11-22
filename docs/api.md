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

A request with an `Authorization` header uses only that header; cookies are ignored. Browser sessions can call the same endpoints with their cookie, but only from the same origin.

Each endpoint needs one scope:

| Scope | Endpoints |
|---|---|
| `items:write` | uploads, pastes, links, `PATCH /items/{id}` |
| `items:read` | `GET /items`, `GET /items/{id}` |
| `items:delete` | `DELETE /items/{id}` |

Creating anything also needs a verified email address (`403 Verify your email address first.`).

## Responses

Success bodies are JSON. Errors are JSON too: `{"error": "Message."}` with a 4xx or 5xx status.

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
- `thumbnail_url` is the raw URL for images until a thumbnail exists.
- `deletion_url` deletes the item without signing in (see [Deletion by token](#deletion-by-token)). It is returned only when the item is created.
- `expires_at` is `null` for items that never expire.

## Options

Uploads, pastes and links take the same options. Multipart uploads send them as form fields, raw-body requests as query parameters, JSON requests as fields.

| Option | Values | Default |
|---|---|---|
| `expires` | `never`, `1h`, `1d`, `7d`, `30d`, `90d`, or an RFC 3339 time | your default |
| `expiry_action` | `delete` or `private` (hide instead of delete) | `delete` |
| `visibility` | `unlisted` (anyone with the link) or `private` (only you) | your default |
| `password` | a password visitors must enter | none |
| `naming` | `random`, `words` or `original` | your default |
| `length` | 8–32, for `random` | your default (8) |
| `slug` | a custom name: 1–64 of `A-Z a-z 0-9 - _` | — |
| `title` | display title | — |

Raw-body requests never take the password in the query string; send it in the `X-Snap-Password` header.

Names are case-insensitive and unique across files, pastes and links. A taken custom name returns `409`; a reserved one (`api`, `login`, `dashboard`…) returns `400`.

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

Fields: `content` (required), `lang`, `name`, plus the [options](#options). The language is `lang` if given, else the one the name implies (`main.go` → Go), else plain text. Markdown pastes (`lang: markdown` or a `.md` name) show rendered, with a Source view. Pastes are capped at 16 MB or your upload limit, whichever is lower. Text over 512 KB is shown as a raw link instead of highlighted.

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
| `password` | a string sets it; `null` or `""` removes it |
| `slug` | rename; the old URL stops working |

```sh
curl -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" -X PATCH \
  -d '{"expires":"7d","password":null}' $SNAP/api/v1/items/42
```

Changing the password signs out everyone who unlocked the item.

### `DELETE /api/v1/items/{id}`

Deletes the item and its files. `204` on success.

```sh
curl -H "Authorization: Bearer $KEY" -X DELETE $SNAP/api/v1/items/42
```

## Deletion by token

Every item gets a deletion URL `https://…/x#<token>` when it is created (ShareX `DeletionURL`). Opening it shows what will be deleted and asks to confirm. The token stays in the URL fragment, so it never reaches access logs.

The page uses two public endpoints, which scripts can call too (rate limited per address):

```sh
curl -H "Content-Type: application/json" -d '{"token":"Zq3…"}' $SNAP/api/v1/delete/lookup
# {"name":"photo.png","kind":"file","type":"png","size":48213,"size_text":"48.2 KB","created_at":"…","url":"…"}

curl -H "Content-Type: application/json" -d '{"token":"Zq3…"}' $SNAP/api/v1/delete
# {"deleted":true}
```

## Content URLs

| URL | Serves |
|---|---|
| `/{slug}` | share page; short links redirect |
| `/{slug}.{ext}`, `/{slug}/raw` | the content inline (the extension is ignored) |
| `/{slug}/download` | the content as an attachment |
| `/{slug}/thumb` | thumbnail, once one exists |
| `/{slug}/preview` | video hover teaser, once one exists |

Private items open only for their owner; everyone else gets `404`, as for expired and deleted items. Password-protected content answers `403` until the visitor unlocks it on the share page. Content supports `Range` and `If-None-Match`. See [uploads.md](uploads.md) for how content is served safely.
