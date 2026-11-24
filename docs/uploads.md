# Uploads and sharing

How files, pastes, gists and short links are stored, limited, named, protected and served. The HTTP endpoints are in [api.md](api.md).

## Ways to upload

- **Browser.** The composer at `/` (Upload, Paste and Link tabs). Drop files anywhere on the window or browse. Files go up in 8 MiB parts and resume from the server's offset after a dropped connection. Each file has a row that shows its progress, then (while the server finishes the file) "processing", and can be canceled, retried or removed; a file that fails says why, and a retry carries on from the server's offset. **Cancel all** and a warning before leaving the page mid-transfer are there too. When two or more files have uploaded since the prompt was last answered, the Upload tab offers once to put them in a new album, in the order they were picked, with the current options. In the Paste tab, **Add file** adds another file to the paste, which makes it a gist.
- **ShareX and curl.** `POST /api/v1/upload` (multipart, field `file`) and `PUT /api/v1/upload/{filename}` (`curl -T`). Multipart bodies are streamed part by part straight into the store; nothing is spooled to `/tmp`.
- **Pastes and links.** `POST /api/v1/pastes` and `POST /api/v1/links`.

Creating content needs a verified email address.

## Limits

| Setting | Default | Meaning |
|---|---|---|
| `uploads.max_bytes` | 1 GB (1000000000) | largest single file |
| `uploads.default_quota_bytes` | 0 (unlimited) | storage per user |
| `uploads.blocked_extensions` | none | e.g. `["exe", "bat"]` |
| `uploads.blocked_types` | none | sniffed MIME types, e.g. `["application/x-msdownload", "video/*"]` |
| `items.max_expiry_days` | 0 (no cap) | caps every expiry, including "never" |
| `items.slug_cooldown_days` | 90 | days a freed name stays reserved for its previous owner |
| `storage.min_free_bytes` | 1 GiB | uploads fail with `507` rather than fill the disk past this |

To raise the file limit, set **Max file size** in Admin › Settings (or `snap-server admin settings set uploads.max_bytes 4000000000`) and raise the proxy's body limit above it: in the example deployment, `CADDY_MAX_BODY=4GiB` in `.env`, then `docker compose up -d caddy`. There is no upper bound in snap itself; the disk (`storage.min_free_bytes`) and quotas still apply.

A user's `max_upload_bytes` lowers the file limit for that user; `quota_bytes` replaces the default quota (0 = unlimited). Pastes are capped at 16 MB.

**Rate.** One account creates at most 120 items a minute (bursts of 60), uploads, pastes, gists and links together (a gist counts once), from browsers and keys alike; past that it gets `429` with `Retry-After`. Addresses in `SNAP_RATELIMIT_EXEMPT` are not limited.

**Quota.** Space is reserved when an upload starts: the declared size for resumable uploads, `Content-Length` for the others, or the remaining quota when the length is unknown. Concurrent uploads therefore cannot overshoot the quota. On completion the reservation becomes usage of the actual size; aborted, failed and abandoned uploads release it. The cached render of text and code (the highlighted HTML) counts too; a render that does not fit the quota is not kept, and the share page shows plain text instead. Each short link counts 1 KiB. An hourly job recomputes usage from the stored items. Thumbnails and teasers do not count.

## Names

One namespace covers files, pastes and links. Names are `A-Z a-z 0-9 - _` and compared case-insensitively, so `/Report` and `/report` are the same item. Reserved, under any extension and in any case:

- names of app paths (`api`, `login`, `dashboard`, `static`…);
- files other services look for at a site's root: domain verification (`google<hex>`, `BingSiteAuth`, `yandex_…`, `baidu_verify_…`, `pinterest-…`, `mywot…`, `keybase`), policies (`ads`, `app-ads`, `sellers`, `security`, `humans`, `crossdomain`, `clientaccesspolicy`, `apple-app-site-association`) and anything starting with `sitemap`. `/.well-known/` is reserved as a whole. These answer `404` even if an older item has the name.

| Scheme | Example | Notes |
|---|---|---|
| `random` (default) | `k3x9qm2v7a` | lowercase letters and digits, 10–32 characters (default 10, about 52 bits). Names match regardless of case, so mixed case would add nothing. |
| `words` | `brisk-amber-otter` | three words from the EFF large list, about 39 bits |
| `original` | `Screen-Shot` | the file name without its extension; `-2`, `-3`… on collision. Guessable. |
| custom | `docs` | any free, unreserved name |

Each user sets a default scheme, random length, visibility and expiry at **Settings › Defaults**; every upload can override them.

**Freed names.** When an item is deleted, expires with the `delete` action or is renamed, its name stays reserved for its owner for `items.slug_cooldown_days` (90 days): only they can use it again, so a link already shared never leads to someone else's content. Names freed by an admin's removal, or with a deleted account, are held for nobody. The hourly cleanup drops holds past the cooldown.

**Unknown names.** Every `404` in this namespace (`/{name}`, `/{name}.{ext}`, `/{name}/raw`, `/download`, `/thumb`, `/preview`, album names; unknown, private, expired or reserved alike) spends one of 30 a minute for the client's address (an IPv6 /64 counts as one) and one of 1200 a minute for all addresses together. Past either, misses get `429` with `Retry-After`; names that exist still open. Addresses in `SNAP_RATELIMIT_EXEMPT` are not limited.

## Types

The stored type comes from the file's first bytes (magic numbers, then Go's content sniffing), never from the name or the client. The name only refines plain text (for example `.md` → Markdown, `.js` → JavaScript). File names are NFC-normalised, stripped of control characters, bidi overrides and path separators, and cut to 255 bytes. Blobs are stored under random keys, never under the file name.

## Protection and expiry

- **Unlisted** items open for anyone with the link. **Private** ones open only for their owner; everyone else gets `404`. On share pages, raw URLs and album pages the owner is their browser session or one of their API keys with `items:read`; a key without it (a ShareX key, an `account:read` key) is treated like any visitor, and the home page treats every key as signed out.
- **Passwords** are at least 8 characters (items made with shorter ones before this rule still unlock). Visitors enter the password on the share page. Unlocks are kept for 24 hours in one signed `__Host-snap_unlock` cookie that holds up to 20 items. Changing an item's password ends its unlocks. Attempts are limited to 5 a minute per address and item and 20 a minute per address; after 20 wrong passwords in an hour, from any addresses, the item refuses every attempt (`429`) until the hour has passed. Unlocks hash on their own two slots of the password hasher, so they cannot hold up sign-in.
- **Expiry** is checked on every request, so an expired item is gone the moment it expires. A sweeper then applies the expiry action each minute: `delete` removes the item and its files, `private` hides it and clears the expiry.
- **Deletion URLs** (`/x#token`) delete an item without signing in. The token is shown once, at creation.

## How content is served

Share pages (`/{slug}`) show images filling the frame (click a smaller image for its actual size, a larger one for the full-size file), video and audio players, highlighted text and code, Markdown (Rendered | Source), and a download card for everything else. Code is highlighted once at upload and cached; text over 512 KB gets a raw link instead.

Rendering limits (`internal/render`), so that hostile text cannot tie up the server:

- Highlighting covers text up to 256 KB and gets 5 s. Larger text, text that takes longer, and text with a line over 20,000 characters (minified code, one-line data) is shown plain.
- Markdown is rendered up to 128 KB, in at most 5 s. Larger documents, and documents with a line over 20,000 characters, blockquotes or lists nested more than 32 deep on one line, or tables with more than 50,000 cells in total, show the Source view only (with the raw link). Code blocks still being highlighted after 3 s are shown plain.
- At most two renders run at once in the server. A render that misses its deadline is abandoned but keeps its slot until it finishes, so stuck work cannot pile up.
- Lexers that cannot be interrupted or that pick another language from the content are not used; that text is shown plain: ERB, Svelte, Go HTML templates, YAML+Jinja, HTTP, reStructuredText, Org, Gemtext, Markless, Raku, Arturo, CQL, PostgreSQL and Materialize SQL. PHP-in-HTML (`.php`, `phtml`) is highlighted with the PHP lexer. Markdown source is highlighted unless a code fence names one of these.

Raw responses (`/{slug}.{ext}`, `/raw`, `/download`, `/thumb`, `/preview`) are isolated from the app:

- `Content-Security-Policy: sandbox; default-src 'none'; …` gives the response an opaque origin, so even an HTML or SVG file cannot run script or read cookies.
- HTML, XHTML, XML, JavaScript, CSS, JSON, WebAssembly and other text are sent as `text/plain; charset=utf-8`, so an upload can never become a page, script, style or service worker on the app origin. SVG keeps `image/svg+xml` (the sandbox blocks its scripts). Types that are not known to be safe are sent as `application/octet-stream`.
- `X-Content-Type-Options: nosniff`, `X-Robots-Tag: noindex`, `Cross-Origin-Resource-Policy: cross-origin`, and an RFC 5987 `Content-Disposition` (inline for raw, attachment for download).
- PDFs go out without `sandbox` (Chromium will not render sandboxed PDFs) but keep `default-src 'none'`. Without a raw origin the share page offers Open (new tab) and Download instead of an inline preview.
- `Range` requests work (video seeking, resumed downloads). The ETag is the file's SHA-256.

### Separate raw origin

Set `SNAP_RAW_URL` (ideally a different registrable domain) to serve content from there. Raw, download, thumbnail and teaser URLs then point at the raw origin, and requests for them on the app origin redirect there.

The raw origin serves nothing else. `GET` and `HEAD` of any other path (share pages, sign-in, the dashboard, `/s/…`, the API, static files) redirect to the same path on `SNAP_PUBLIC_URL`; other methods get `404`. Requests there are always anonymous: cookies and `Authorization` headers are dropped before the app sees them, and no response from it sets a cookie.

Cookies do not reach the raw origin, so private and password-protected items get signed URLs (`?e=<expiry>&s=<signature>`, HMAC with a key derived from `SNAP_SECRET_KEY`) when an authorised viewer loads the share page or follows a raw link on the app origin. They last 6–7 hours, are bound to the item by a random per-item value (never valid for another item, even one with the same id) and stop working when the item's password or visibility changes. With a raw origin, PDFs are previewed inline in a frame from that origin.

## Gists

A gist is one item with one link that holds several text files, shown together on one page. Create one with [`POST /api/v1/gists`](api.md#gists-post-apiv1gists) or `sp paste a.go b.md`.

- 1 to 50 files, at most 16 MB together (or your upload limit, if lower). Every file needs some non-blank text.
- Names are cleaned like file names, and blocked extensions are refused. A file without a name becomes `file<n>` with its language's extension (`file2.py`), else `.txt`. Two files can't share a name, ignoring case.
- The language is the one given, else the one the name implies, else plain text, as for pastes. Markdown files show rendered, with a Source view.
- The page shows the title (or the first file's name) with Download zip, then each file with Raw and Download. Each file is highlighted once, at creation, within the [rendering limits](#how-content-is-served); highlighting a whole gist gets 20 s. A file without a kept render (too large, over the quota, past those 20 s) or past the first 8 MB of renders on the page shows a raw link instead.
- Files are `/{slug}/raw?f=N` and `/{slug}/download?f=N`, N counting from 0, with the same headers as pastes. `/{slug}/download` is a zip of all the files (stored, not compressed). On a separate raw origin, the signature of a protected gist's URLs is valid for all its files (`f` is not signed).
- The files count toward the quota together: the whole gist fits or nothing is stored. Their renders count too, and one that does not fit is not kept. A gist is one item toward the rate limit.
- Search covers the title, every file name and the start of each file. Title, expiry, visibility, password and name can be changed; the files can't.
- Views count on the gist, and downloads of a file or of the zip count as its downloads.

## Storage

Blobs live in `SNAP_FILES_DIR` as `ab/cd/<32 hex>`. Writes go to `.tmp/` first and are renamed into place after an fsync of the file and its directory, so a crash never leaves a partial blob under a key. Temp files untouched for 24 hours are removed hourly. Every path is opened through an `os.Root`, so no key can reach outside the directory.

Deleted files remain in filesystem snapshots (ZFS) until those snapshots age out.

## Counting

Share page views, link clicks and downloads are counted in memory and written every 10 seconds. The owner's own requests, bots, chat unfurlers, prefetches and Range requests past the start of a file are not counted (`stats.Classify`). Nor are subresource loads made by the app's own pages (`Sec-Fetch-Dest` such as `image` with a same-origin request or Referer): a Markdown image pointing at `/{slug}/download` or a raw URL would otherwise count every visitor of the page that shows it. The first counted open sets the item's "opened" badge. "Your own requests" means a browser session of the owner or an owner's key with `items:read`; other keys count as visitors.

Item ids are never reused, and visitor events of an item deleted before they are written are dropped, so a new item never inherits an old one's visitors or notifications.
