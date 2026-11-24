# One-time secrets

A secret is a text or a file that can be opened a set number of times (1 by default) and is then destroyed. It is encrypted before it leaves your machine: the key is in the link's fragment (`https://host/s/{id}#{key}`), which browsers never send to the server. An optional passphrase adds a second factor that travels separately.

What the server stores and sees in each mode, and what it can't protect against, is in [security.md](security.md#one-time-secrets-server-behaviour-and-threat-model).

## In the browser

Open the Secret tab of the composer (`/#panel-secret`, or **New secret** on the Secrets page in the sidebar). Signed-out visitors use `/new/secret` when anonymous secrets are on.

1. Choose **Text** or **File** (files up to `secrets.max_bytes`, 256 MiB by default).
2. Pick the views (1, 2, 3, 5, 10 or a custom number up to `secrets.max_views`) and the expiry (5 minutes to 30 days).
3. Optionally set a passphrase. **Generate passphrase** makes six words from the EFF list (about 77 bits); **Random characters** makes 22 letters and digits (about 128 bits). Send the passphrase another way than the link.
4. Optionally add a note. It is for your secrets page only: the server can read it, the recipient can't see it.
5. **Create secret**. The link is shown once; copy it then.

The page needs https (or localhost) and JavaScript: WebCrypto is not available otherwise, and nothing is sent until the page's script has loaded.

The recipient opens the link, sees the kind, size, expiry and views left, and clicks **Reveal**. Only Reveal uses a view; loading the page does not. Text is shown with a Copy button. Files are decrypted in the browser and offered for download; PNG, JPEG, GIF, WebP, AVIF, MP4, WebM and audio files also get a **Preview** button (nothing is decoded until it is clicked). A link that has been used says "Already opened and gone" with when it was first opened ("Opened 2026-11-22 14:03"), or that it expired, was revoked, or was destroyed after wrong passphrases.

**Keeping the link out of browser history.** A browser records the full address, key included, in its history as soon as a link is opened, before the page can remove it. To avoid that, open `https://share.example.com/s` and paste the link into the box there: it is opened on that page and never becomes a history entry. `sp open` avoids the browser altogether.

**Links sent by email.** Some mail security scanners (Safe Links, Proofpoint) open links to check them. They don't click Reveal, but use a passphrase for links you email, and check the tombstone or your secrets page if a recipient says the link was already used.

## With `sp`

`sp` encrypts on your machine, so it is end-to-end even against a server that has been tampered with.

```sh
sp secret notes.txt                        # a file, 1 view, expires in 1 day
echo 'db password: hunter2' | sp secret    # text from stdin (up to 1 MiB)
sp secret backup.tar --views 3 --expire 7d -p        # asks for a passphrase twice
sp secret key.pem --passphrase-file pw.txt --note "for Sam"
```

The link goes to stdout, everything else to stderr. `sp secret` needs `sp login` (or `SNAP_URL` and `SNAP_KEY`) with a key that has `secrets:write`.

```sh
sp open 'https://share.example.com/s/…#…'          # text to stdout; a file is saved under its name
sp open 'https://share.example.com/s/…#…' -o out.bin
sp open 'https://share.example.com/s/…#…' --passphrase-file pw.txt
```

`sp open` talks to the server in the link, never sends your API key, refuses unsafe passphrase settings from the server before asking for the passphrase, and checks the key commitment before decrypting. It refuses `http://` links unless given `--insecure`. It never overwrites a file: `-o` refuses an existing path before using a view, and a taken name from the secret becomes `name (1).ext`. Files are saved with mode 0600.

On a terminal, control characters in the secret (escape sequences that could set your clipboard, forge links or rewrite the screen) are shown as `^[`, `^G` and so on, with a note on stderr; `--raw` writes the exact bytes, and a pipe or `-o` always gets them. `-o -` writes a file to stdout after decrypting it in memory, never to a temporary file, so it is limited to 64 MiB; use `-o FILE` for larger files (checked before a view is used).

## With curl or ShareX (sealed mode)

Clients that can't encrypt send the plaintext over TLS and the server encrypts it as it streams in, with a key it generates and forgets once it has returned the link. These secrets are labelled **server-encrypted**: the server saw the content while encrypting, so they are only as private as the running server. There is no passphrase in sealed mode. `sp` always does real end-to-end encryption.

```sh
# A file; the response is JSON {url, id, expires_at, views}.
curl -H "Authorization: Bearer $SNAP_KEY" -F file=@photo.jpg -F views=2 -F expires=1h \
  https://share.example.com/api/v1/secrets/sealed

# Text from stdin; just the URL back.
printf 'hunter2' | curl -H "Authorization: Bearer $SNAP_KEY" -H 'Accept: text/plain' \
  --data-binary @- 'https://share.example.com/api/v1/secrets/sealed?views=1&expires=1d'
```

Fields (multipart fields or query parameters): `file` (a file) or `text` (UTF-8 text), `views`, and `expires` (`5m`, `1h`, `1d`, `7d`, `30d`, a Go duration or seconds). A body that is not multipart is taken as text. `?format=url` or `Accept: text/plain` returns the bare URL. For ShareX, use a custom uploader with `Body: MultipartFormData`, `FileFormName: file`, the key in `Headers.Authorization` and `URL: {json:url}`.

## Your secrets page

**Secrets** in the sidebar (`/dashboard/secrets`) lists your secrets with their note (or kind), state, whether and when they were opened, views used, expiry and creation time; secrets with a passphrase and server-encrypted secrets are marked. The note links to the timeline, and each row's menu has **Timeline**, which opens the timeline in a dialog, and **Burn now**, which destroys a secret at once, also for someone still downloading it. The timeline shows when the link was opened, revealed, a wrong passphrase was tried, and when it was burned, with the time, IP address and browser. Your own requests are not recorded, nor is anything for anonymous secrets.

## Limits and settings

| Setting | Default | |
|---|---|---|
| `secrets.max_bytes` | 268435456 (256 MiB) | file size; the browser holds the whole file in memory |
| `secrets.max_views` | 100 | |
| `secrets.max_expiry` | 2592000 (30 days) | seconds; the minimum expiry is 5 minutes, the default 1 day |
| `secrets.max_failures` | 10 | wrong passphrases before a secret is destroyed |
| `secrets.anonymous` | false | let visitors without an account create text secrets |
| `secrets.anonymous_max_bytes` | 65536 (64 KiB) | anonymous text size after padding |
| `secrets.anonymous_max_total_bytes` | 268435456 (256 MiB) | ciphertext of all live anonymous secrets together |
| `secrets.anonymous_per_hour` | 60 | anonymous creates per hour, from all addresses together |

Secrets count against your storage quota like uploads: the ciphertext is reserved when you start a secret, counts as used once it is ready, and is given back when it is destroyed (last view, burn, expiry, Burn now) or an unfinished upload is removed. Your per-file upload limit and an API key's upload cap and expiry cap apply too.

Fixed limits: text is at most 1 MiB; at most 5 unfinished uploads per user and per client address; an upload must be finished within an hour. Rate limits: opening a secret (info and claim) 60 per minute per client address with a burst of 30, and 120 per minute per secret; creating 300 per hour per user (burst 60); anonymous creating 10 per hour per client address (burst 3). A client address is an IPv4 address or an IPv6 /64, since one IPv6 client usually has a whole /64. `SNAP_RATELIMIT_EXEMPT` lifts the per-address limits and caps; it matches full addresses.

## Lifecycle

- **uploading**: created, chunks arriving. Can't be opened. Removed after an hour if not finished.
- **ready**: can be opened until its views are used or it expires.
- **burned**: the last view was used, or too many wrong passphrases were tried. The owner gets an email for the second case, when mail is configured and their address is verified.
- **expired**, **revoked** (burned by the owner or an admin).

The ciphertext of a text secret is deleted as soon as its last view has been sent. A file's ciphertext is kept for the download: until the recipient's browser (or `sp`) reports that it decrypted the whole file, or until the download ticket expires (15 minutes, or longer for files that take longer than that at 1 Mbit/s). The sweeper runs every minute. Tombstones (state, first-open time and whether there was a passphrase; no IPs and nothing that could check a passphrase) are kept for 30 days, then deleted with their events.

## API

All binary values are unpadded base64url. Ids, probes, tokens and tickets never go in a URL query, and none of these routes log their path.

| Request | Who | |
|---|---|---|
| `POST /api/v1/secrets` `{kind, size, views, expires_in, note, notify}` | `secrets:write`, verified email; anyone when `secrets.anonymous` is on | `size` is the plaintext size (text: padded to a power of two from 1 KiB). Returns `{id, upload_token, chunk_size, chunks, ct_size, expires_at}`. |
| `PUT /api/v1/secrets/{id}/chunks/{n}` | `X-Snap-Upload-Token` | Chunk `n` of the STREAM ciphertext, in order; 1048592 bytes except the last. Resending a stored chunk with the same bytes is fine; different bytes get 409. |
| `POST /api/v1/secrets/{id}/finalize` `{salt, kdf, h_probe, h_auth, commit, meta_ct}` | `X-Snap-Upload-Token` | `kdf` is `{m, t, p}` or `null`; `meta_ct` is 1052 bytes. The secret becomes ready. |
| `POST /api/v1/secrets/{id}/info` `{probe}` | link holders | Ready: `{state, mode, kind, salt, kdf, size, ct_size, views_left, expires_at}`. Gone: 410 `{state, opened_at, burn_reason}`. Wrong or missing probe: the same 404 as an unknown id. |
| `POST /api/v1/secrets/{id}/claim` `{probe, auth}` | link holders | Uses a view. 200 `{kind, commit, meta_ct, views_left, ct_size}` plus `content` (text ciphertext) or `ticket` and `ticket_expires_at` (files). Wrong passphrase: 403 `{error, attempts_left, state}`; on a secret without a passphrase the same 403 is not counted as an attempt. |
| `GET /api/v1/secrets/{id}/blob` | `X-Snap-Ticket` | The file ciphertext; Range requests work. A ticket serves at most 2 × chunks + 16 requests and 3 × `ct_size` bytes, then 410. |
| `POST /api/v1/secrets/{id}/done` | `X-Snap-Ticket` | After the last chunk verified. Ends the ticket; deletes the file if no views are left. |
| `POST /api/v1/secrets/sealed` | `secrets:write`, verified email | See sealed mode. |
| `GET /api/v1/secrets?limit=&offset=` | `secrets:read` | Your secrets, metadata only. |
| `GET /api/v1/secrets/{id}/events` | `secrets:read`, owner | The timeline. |
| `DELETE /api/v1/secrets/{id}` | `secrets:write`, owner | Burn now. |

The envelope format (keys, chunks, metadata) is specified in [security.md](security.md#e2e-envelope-v1).
