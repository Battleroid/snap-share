# Uploads and sharing

How files, pastes and short links are stored, limited, named, protected and served. The HTTP endpoints are in [api.md](api.md).

## Ways to upload

- **Browser.** The composer at `/` (Upload, Paste and Link tabs). Drop files anywhere on the window or browse. Files go up in 8 MiB parts and resume from the server's offset after a dropped connection.
- **ShareX and curl.** `POST /api/v1/upload` (multipart, field `file`) and `PUT /api/v1/upload/{filename}` (`curl -T`). Multipart bodies are streamed part by part straight into the store; nothing is spooled to `/tmp`.
- **Pastes and links.** `POST /api/v1/pastes` and `POST /api/v1/links`.

Creating content needs a verified email address.

## Limits

| Setting | Default | Meaning |
|---|---|---|
| `uploads.max_bytes` | 1 GiB | largest single file |
| `uploads.default_quota_bytes` | 0 (unlimited) | storage per user |
| `uploads.blocked_extensions` | none | e.g. `["exe", "bat"]` |
| `uploads.blocked_types` | none | sniffed MIME types, e.g. `["application/x-msdownload", "video/*"]` |
| `items.max_expiry_days` | 0 (no cap) | caps every expiry, including "never" |
| `storage.min_free_bytes` | 1 GiB | uploads fail with `507` rather than fill the disk past this |

A user's `max_upload_bytes` lowers the file limit for that user; `quota_bytes` replaces the default quota (0 = unlimited). Pastes are capped at 16 MB.

**Quota.** Space is reserved when an upload starts: the declared size for resumable uploads, `Content-Length` for the others, or the remaining quota when the length is unknown. Concurrent uploads therefore cannot overshoot the quota. On completion the reservation becomes usage of the actual size; aborted, failed and abandoned uploads release it. An hourly job recomputes usage from the stored items. Thumbnails and teasers do not count.

## Names

One namespace covers files, pastes and links. Names are `A-Z a-z 0-9 - _` and compared case-insensitively. Names of app paths (`api`, `login`, `dashboard`, `static`…) are reserved.

| Scheme | Example | Notes |
|---|---|---|
| `random` (default) | `aB3dE9xQ` | base62, 8–32 characters (default 8) |
| `words` | `brisk-amber-otter` | three words from the EFF large list, about 39 bits |
| `original` | `Screen-Shot` | the file name without its extension; `-2`, `-3`… on collision. Guessable. |
| custom | `docs` | any free, unreserved name |

Each user sets a default scheme, random length, visibility and expiry at **Settings › Defaults**; every upload can override them.

## Types

The stored type comes from the file's first bytes (magic numbers, then Go's content sniffing), never from the name or the client. The name only refines plain text (for example `.md` → Markdown, `.js` → JavaScript). File names are NFC-normalised, stripped of control characters, bidi overrides and path separators, and cut to 255 bytes. Blobs are stored under random keys, never under the file name.

## Protection and expiry

- **Unlisted** items open for anyone with the link. **Private** ones open only for their owner; everyone else gets `404`.
- **Passwords.** Visitors enter the password on the share page. Unlocks are kept for 24 hours in one signed `__Host-snap_unlock` cookie that holds up to 20 items. Changing an item's password ends its unlocks. Attempts are rate limited per address and item.
- **Expiry** is checked on every request, so an expired item is gone the moment it expires. A sweeper then applies the expiry action each minute: `delete` removes the item and its files, `private` hides it and clears the expiry.
- **Deletion URLs** (`/x#token`) delete an item without signing in. The token is shown once, at creation.

## How content is served

Share pages (`/{slug}`) show images (click for full size), video and audio players, highlighted text and code, Markdown (Rendered | Source), and a download card for everything else. Code is highlighted once at upload and cached; text over 512 KB gets a raw link instead.

Raw responses (`/{slug}.{ext}`, `/raw`, `/download`, `/thumb`, `/preview`) are isolated from the app:

- `Content-Security-Policy: sandbox; default-src 'none'; …` gives the response an opaque origin, so even an HTML or SVG file cannot run script or read cookies.
- HTML, XHTML, XML, JavaScript, CSS, JSON, WebAssembly and other text are sent as `text/plain; charset=utf-8`, so an upload can never become a page, script, style or service worker on the app origin. SVG keeps `image/svg+xml` (the sandbox blocks its scripts). Types that are not known to be safe are sent as `application/octet-stream`.
- `X-Content-Type-Options: nosniff`, `X-Robots-Tag: noindex`, `Cross-Origin-Resource-Policy: cross-origin`, and an RFC 5987 `Content-Disposition` (inline for raw, attachment for download).
- PDFs go out without `sandbox` (Chromium will not render sandboxed PDFs) but keep `default-src 'none'`. Without a raw origin the share page offers Open (new tab) and Download instead of an inline preview.
- `Range` requests work (video seeking, resumed downloads). The ETag is the file's SHA-256.

### Separate raw origin

Set `SNAP_RAW_URL` (ideally a different registrable domain) to serve content from there. Raw, download, thumbnail and teaser URLs then point at the raw origin, and requests for them on the app origin redirect there. Share pages requested on the raw origin redirect to the app.

Cookies do not reach the raw origin, so private and password-protected items get signed URLs (`?e=<expiry>&s=<signature>`, HMAC with a key derived from `SNAP_SECRET_KEY`) when an authorised viewer loads the share page or follows a raw link on the app origin. They last 6–7 hours and stop working when the item's password or visibility changes. With a raw origin, PDFs are previewed inline in a frame from that origin.

## Storage

Blobs live in `SNAP_FILES_DIR` as `ab/cd/<32 hex>`. Writes go to `.tmp/` first and are renamed into place after an fsync of the file and its directory, so a crash never leaves a partial blob under a key. Temp files untouched for 24 hours are removed hourly. Every path is opened through an `os.Root`, so no key can reach outside the directory.

Deleted files remain in filesystem snapshots (ZFS) until those snapshots age out.

## Counting

Share page views, link clicks and downloads are counted in memory and written every 10 seconds. The owner's own requests, bots, chat unfurlers, prefetches and Range requests past the start of a file are not counted (`stats.Classify`). The first counted open sets the item's "opened" badge.
