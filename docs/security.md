# Security

## E2E envelope v1

This section is the normative format for one-time secrets. The reference implementations are `internal/e2e` (Go: `sp` and the server's sealed mode) and `internal/web/static/js/e2e.js` (browser). Both are tested against the shared vectors in `internal/e2e/testdata/vectors.json`. MUST and MUST NOT mark rules that every reader and writer follows.

### Notation

- `||` is concatenation. Sizes are in bytes.
- `b64(x)` is base64url (RFC 4648 §5) without padding. Decoders MUST reject padding, characters outside the alphabet, the wrong length and non-zero trailing bits, so every value has exactly one encoding.
- `lp(label)` is one byte holding the label's length, followed by the ASCII label. `lp("auth")` is `04 61 75 74 68`.
- `HKDF` is HKDF-SHA256 (RFC 5869). `Argon2id` is RFC 9106, version 0x13. `AES-GCM` is AES-256-GCM with a 12-byte nonce, a 16-byte tag and no associated data.
- `L1` is the 11-byte ASCII string `snap-e2e-v1`.

### Values

| Value | Size | Made by | The server |
|---|---|---|---|
| `id` | 16 | server, random | knows it |
| `K_link` | 32 | creator's CSPRNG, new for every create attempt | never sees it |
| `S` (envelope salt) | 16 | creator's CSPRNG | stores it |
| passphrase | any | creator, optional | never sees it |
| KDF params `m`, `t`, `p` | — | creator | stores them |
| `K_probe` | 32 | derived | stores SHA-256(`K_probe`); receives `K_probe` on every info and claim request |
| `K_auth` | 32 | derived | stores SHA-256(`K_auth`); receives `K_auth` on claim |
| `K_enc`, `K_meta` | 32 each | derived | never sees them |
| `K_commit` | 32 | derived | stores it; returns it on claim |

### Link

```
{base}/s/{b64(id)}#{b64(K_link)}
```

The id is 22 characters and the key 43. The key is in the fragment, so browsers never send it to the server. Writers drop trailing slashes from `base`. Readers trim surrounding whitespace, split at the first `#`, and take `base` as everything before the last `/s/`; anything else between `/s/` and `#` (a query, a trailing slash) makes the id invalid.

### Key derivation

```
K_probe  = HKDF(IKM = K_link, salt = L1, info = lp("probe") || id, L = 32)

K_pw     = Argon2id(password = UTF-8(NFC(passphrase)), salt = S, t, m KiB, p = 1, length 32)
PRK      = HKDF-Extract(salt = L1 || S, IKM = K_link || K_pw)      IKM = K_link without a passphrase
K_auth   = HKDF-Expand(PRK, lp("auth")   || id, 32)
K_enc    = HKDF-Expand(PRK, lp("enc")    || id, 32)
K_meta   = HKDF-Expand(PRK, lp("meta")   || id, 32)
K_commit = HKDF-Expand(PRK, lp("commit") || id, 32)
```

Every `info` is `lp(label)` followed by the 16 raw id bytes. `K_probe` needs only the link, so a client can compute it before it knows `S`. It proves the requester holds the link: someone who only learns the id (from access logs or a proxy) cannot read the secret's info or burn it.

HMAC pads keys shorter than its block with zeros, so the two HKDF-Extract salts `L1` and `L1 || S` give the same PRK when `S` is all zeros and there is no passphrase: the main PRK then equals the probe PRK. This has no effect, because every output is expanded with its own `info` label, so no derived key equals `K_probe` (tested in `internal/e2e`). `S` is random in any case.

Passphrase rules:

- The passphrase MUST be non-empty and well-formed Unicode (valid UTF-8 in Go, no lone surrogates in JS). It is normalised to NFC, not NFKC, before UTF-8 encoding, so composed and decomposed input match but compatibility characters (`ﬁ` and `fi`) stay distinct. NFC can differ between Unicode versions for newly encoded characters, so Go (`golang.org/x/text`) and a browser (its ICU) may normalise such a passphrase differently; passphrases from the generators and in established scripts are unaffected.
- Defaults: `m` = 65536 KiB (64 MiB), `t` = 3, `p` = 1.
- Readers MUST refuse parameters outside `m` ∈ [65536, 262144], `t` ∈ [3, 10], `p` = 1 before running Argon2id. The parameters come from the server; without the check a malicious server could lower the cost to make offline guessing cheaper, or make the browser allocate gigabytes. The floor is the default: every creator uses it, so nothing legitimate asks for less.

### Key commitment

AES-GCM does not commit to its key: a ciphertext can be built that decrypts under two different keys. The reader MUST compare its derived `K_commit` with the value the server returns, in constant time, and stop on a mismatch before decrypting metadata or content. A mismatch means a wrong passphrase, a wrong link, or a tampered `S` or KDF parameters.

### Content: STREAM over AES-256-GCM

The plaintext is split into chunks of 1048576 bytes (1 MiB). Chunk `i`, counting from 0, is encrypted under `K_enc`:

```
nonce_i = BE88(i) || flag          12 bytes
          bytes 0..10  i as an unsigned 11-byte big-endian integer
          byte  11     0x01 on the final chunk, 0x00 otherwise
ct_i    = AES-GCM(K_enc, nonce_i, chunk_i)     chunk_i || 16-byte tag
content = ct_0 || ct_1 || … || ct_last
```

- Every non-final chunk is exactly 1048576 bytes, so every non-final `ct_i` is 1048592 bytes.
- The final chunk holds 0 to 1048576 bytes; it may be full. It is empty only when it is the only chunk: empty plaintext is exactly one empty final chunk (16 bytes of ciphertext). So each plaintext has exactly one encoding, of size `n + 16 × max(1, ⌈n / 1048576⌉)`.
- The Go counter is a uint64 (bytes 0..2 of the nonce are always zero); JS counters stop at 2^53 − 1. Implementations MUST fail rather than wrap the counter.

Readers cut the ciphertext into 1048592-byte pieces. A piece is the final chunk exactly when the input ends after it. Decryption MUST fail when any tag fails, when the input is empty or the final piece is shorter than 16 bytes, and when an empty final chunk follows other chunks. This makes reordered, dropped, duplicated, truncated (no final flag), extended (data after the final chunk) and modified ciphertext fail, as does the wrong key.

Each chunk is verified before its plaintext is released, but truncation is only detected at the end. Callers MUST treat the output as untrusted until the whole stream has decrypted without error, and discard it otherwise; in the browser, build the download Blob or show the text only after the last chunk verifies.

A chunk MUST NOT be encrypted twice. An upload retry resends the bytes produced the first time: encrypting different plaintext under the same key and counter reuses a GCM nonce, which breaks both confidentiality and integrity. Because `K_link` is new for every create attempt, `K_enc` is never shared between secrets.

### Text padding

Text secrets are UTF-8, zero-padded before encryption to the next power of two that is at least 1024 bytes (1024, 2048, 4096, …). The real length is the metadata `size`; the reader keeps the first `size` bytes. Files are not padded, so the ciphertext size gives away the exact file size.

### Metadata

The metadata plaintext is exactly 1024 bytes:

```
offset   size       field
0        2          n, uint16 big-endian, 1..1022
2        n          JSON, UTF-8
2 + n    1022 − n   zero bytes
```

The JSON is `{"v":1,"kind":…,"name":…,"type":…,"size":…}`:

| Field | Value |
|---|---|
| `v` | `1` |
| `kind` | `"text"` or `"file"` |
| `name` | file name; `""` for text |
| `type` | MIME type as given by the creator, e.g. `"text/plain; charset=utf-8"` |
| `size` | unpadded length in bytes, an integer in [0, 2^53 − 1] |

Writers emit the five keys in that order with no whitespace, and escape strings exactly as ECMAScript `JSON.stringify` does: `"` as `\"`, `\` as `\\`, U+0008, U+0009, U+000A, U+000C, U+000D as `\b \t \n \f \r`, other U+0000–U+001F as `\u00xx` (lowercase hex), everything else as literal UTF-8 (including `/`, `<`, `>`, `&`, U+2028 and U+2029). Invalid UTF-8 and lone surrogates become U+FFFD. If the JSON is longer than 1022 bytes, the name is cut to the longest prefix of whole code points that fits; if it does not fit with an empty name, sealing fails.

Sealed metadata is 1052 bytes:

```
meta_ct = nonce || AES-GCM(K_meta, nonce, plaintext)      12 + 1024 + 16 bytes
```

The nonce is 12 random bytes; each `K_meta` encrypts one record.

Readers MUST reject a length other than 1052, a failed tag, `n` = 0 or `n` > 1022, non-zero padding, invalid UTF-8, a leading byte order mark, JSON that is not an object, `v` that is not the number 1, an unknown `kind`, a `name` or `type` that is not a string, and a `size` that is not an integer in [0, 2^53 − 1]. Unknown keys are ignored. Numbers are compared by value, so `1.0` is `1`.

### What the server learns

- The id, `S`, the KDF parameters (so whether there is a passphrase), SHA-256(`K_probe`), SHA-256(`K_auth`) and `K_commit`.
- The ciphertext size: the exact size for files, the padded size for text.
- When and from where the secret is fetched.

It never receives `K_link`, the passphrase, `K_enc` or `K_meta`. With the database and the link, an attacker can test passphrases offline at the Argon2id cost while the secret is ready; once it is gone the server drops `S`, the KDF parameters, SHA-256(`K_auth`) and `K_commit`. Without the link, the stored values do not help: `K_link` is 256 random bits. In the browser, this holds only as far as the JavaScript the server sends can be trusted; in sealed mode the server generates `K_link` and sees the plaintext while encrypting.

### Implementations and vectors

- Go: `internal/e2e`, using `crypto/hkdf`, `crypto/aes`, `crypto/cipher`, `golang.org/x/crypto/argon2` and `golang.org/x/text/unicode/norm`.
- Browser: `internal/web/static/js/e2e.js`, using WebCrypto for HKDF, SHA-256 and AES-GCM and the vendored hash-wasm 4.13.0 for Argon2id. hash-wasm compiles WebAssembly, so pages that take a passphrase need `'wasm-unsafe-eval'` in `script-src`; it uses no `eval` or `new Function`.
- Vectors: `internal/e2e/testdata/vectors.json` covers key derivation (no passphrase, passphrase, non-NFC passphrase, default KDF cost), links, single chunks, streams (empty, short, padded text, exactly 1 MiB, 1 MiB + 1, multi-chunk), text padding, metadata and complete envelopes, plus negative cases every implementation must reject. Large plaintexts use the generator "byte i = i mod 251" and are checked by SHA-256. Regenerate with `make go ARGS="test ./internal/e2e -run TestVectors -update"`; `make test-go` and `make test-js` check both implementations against it.
- Only the tests choose the metadata nonce (Go `sealMetaNonce`, JS `_testing.sealMetaWithNonce`), to make vectors deterministic. Application code never does.

## One-time secrets: server behaviour and threat model

This section describes what the server does with one-time secrets (`internal/secrets`, `docs/secrets.md`) on top of the envelope above.

### Modes

- **E2E, browser**: the composer and reveal pages encrypt and decrypt with `e2e.js`. The server sends that JavaScript, so this mode is only as end-to-end as the code the server serves at that moment.
- **E2E, `sp`**: `sp secret` and `sp open` use `internal/e2e` on the user's machine. This is end-to-end even against a malicious server: `sp open` checks the KDF bounds before asking for a passphrase, checks the key commitment before decrypting, talks only to the server named in the link, never sends the API key there, and does not follow redirects.
- **Sealed** (`POST /api/v1/secrets/sealed`, for curl and ShareX): the plaintext arrives over TLS; the server generates `K_link` and `S`, encrypts while streaming in the same envelope (no passphrase), returns the link and drops the key. The key is never logged or stored, but the running server saw the plaintext and the key. These secrets are labelled server-encrypted. The plaintext is not sniffed, thumbnailed or otherwise processed; the client's file name and type go only into the sealed metadata.

### What the server stores

| Where | What |
|---|---|
| `secrets` row | id, owner, kind, mode, state; whether there is a passphrase; SHA-256(`K_probe`); while ready only: `S`, KDF parameters, SHA-256(`K_auth`), `K_commit` (cleared on the last view, a burn, Burn now and expiry); ciphertext size and chunk count; the bytes it holds against the owner's quota; views left and maximum, failed attempts and maximum; expiry; the owner's note (plaintext, owner only); the notify flag; first-open time, burn time and reason. While uploading only: SHA-256 of the upload token and the creator's client address (IPv4, or the IPv6 /64; for the open-upload cap), both cleared at finalize. |
| `secret_tickets` | SHA-256 of each download ticket, its secret, expiry, and the requests and bytes it has been served. |
| `secret_blobs` | the blob-store key, size and SHA-256 of each ciphertext file (sealed metadata and content chunks). |
| `secret_events` | for the owner's timeline: time, kind (link opened, revealed, wrong passphrase, burned), IP address, User-Agent and its summary. Deleted after the stats retention period, or with the secret. Anonymous secrets and the owner's own requests record nothing. |
| `inbox` | the owner's in-app notifications (`docs/notifications.md`), for items too: kind, title (the item's name or the secret's note), the visitor's browser summary and IP address, views left, a link to the stats page or the secret's timeline (with the secret's id), time and read time. Deleted after the stats retention period, beyond the newest 200 per user, with a purged secret, and within the hour once the item is gone. |
| `SNAP_SECRETS_DIR` | the sealed metadata (1052 bytes) and the content ciphertext, one file per 1 MiB chunk. |
| `webauthn_credentials` | each passkey or security key: its credential id and public key, the signature counter, transports, the backup-eligible and backed-up flags, a name, when it was added and last used. The private key never leaves the authenticator. |
| `users.webauthn_id` | the account's WebAuthn user handle: 32 random bytes, never the account id. |
| `user_totp` | the authenticator-app secret, sealed (AES-256-GCM under a key derived from `SNAP_SECRET_KEY`, bound to the user id), and the last time step accepted. |
| `recovery_codes` | SHA-256 of a label, the user id and each normalized code; when it was used. The codes themselves are shown once and not kept. |
| `auth_challenges` | for 5 minutes, between the steps of a sign-in or a passkey ceremony: SHA-256 of the token in the browser's cookie, the kind, the user, WebAuthn session data (challenge, allowed credentials), an authenticator-app secret being set up (sealed), a password reset waiting for the second factor (the reset token's hash and the new password's Argon2id hash), where to go next and the failed attempts. |

The server never stores `K_link`, the passphrase, `K_enc`, `K_meta` or any plaintext. No ciphertext is stored in the database.

### What the server sees

- The id on every request; `K_probe` on info and claim; `K_auth` on claim; the upload token while uploading; the ticket while downloading.
- The ciphertext size: padded for text (a power of two, at least 1 KiB), exact for files. File names and types are inside the encrypted metadata.
- When, how often and from which IP address and browser a secret is opened.
- In sealed mode, the plaintext and the key during the upload request.

### What the server enforces

- **Link holders only.** Info and claim need the probe. A wrong or missing probe, an unknown or malformed id, and an unfinished upload all get the same 404 body, and none of them count as a failed attempt. Someone who learns only the id (from a proxy log, say) can neither read the metadata nor burn the secret.
- **One view per claim, atomically.** A claim runs in one `BEGIN IMMEDIATE` transaction that re-checks the probe, state and expiry, then either counts a failed attempt (burning the secret at `secrets.max_failures`, default 10, and notifying the owner) or takes a view. A wrong passphrase never uses a view. 100 parallel claims on a 3-view secret give exactly 3 successes (tested). On a secret without a passphrase, `K_auth` follows from the link, so a wrong one can only come from someone who has the probe but not the link: it gets the same 403 and is not counted, so it can't burn the secret.
- **Expiry on every request.** A ready secret past its expiry is treated as expired before the sweeper notices.
- **Tickets.** A file claim returns a 256-bit ticket, stored hashed, bound to that secret, sent in the `X-Snap-Ticket` header (never in a URL), valid for max(15 minutes, the size at 1 Mbit/s). A ticket serves at most 2 × chunks + 16 requests and 3 × the ciphertext size, then gets 410: enough for retries and resumed ranges, not for handing one claim to many downloaders.
- **No verifier in tombstones.** When a secret leaves ready (last view, failed attempts, Burn now, expiry), `S`, the KDF parameters, SHA-256(`K_auth`) and `K_commit` are set to NULL. Only SHA-256(`K_probe`) stays, so link holders still see what happened; a tombstone can't be used to test passphrases offline.
- **Deletion.** A text secret's ciphertext is deleted right after the response carrying its last view is flushed. A file's ciphertext is deleted when the client reports the last chunk verified (`done`) and no other live ticket exists, otherwise by the sweeper once the last ticket expires; the sweeper never deletes a file a live ticket needs. Burn now (owner or admin) ends live tickets and deletes the files at once. Unfinished uploads are deleted after an hour. Files are tracked in `secret_blobs` without a foreign key, so files of rows deleted any other way (a user deleted with their secrets) are still found and deleted. Tombstones are kept 30 days, then deleted with their events. Deleting a file is an unlink (see the stated limits).
- **Database residue.** SQLite runs with `secure_delete`, which overwrites deleted rows in the database file. In WAL mode the old versions of those pages also sit in `snap.db-wal` until it is reset, so after a burn, Burn now and any sweep that deleted or cleared something, the server runs `PRAGMA wal_checkpoint(TRUNCATE)` (best effort: a busy database delays it to the next one), and `journal_size_limit` keeps the WAL at 4 MiB after checkpoints. Tested by searching both files for a purged row. An id can remain as a key inside an index page after its row is gone; an id alone opens nothing.
- **Quota.** Ciphertext counts against the owner's storage quota and per-file upload limit, like uploads (`uploads.default_quota_bytes`, `users.quota_bytes`, `users.max_upload_bytes`), and an API key's upload and expiry caps apply. The ciphertext size is reserved at create (sealed mode: per chunk as it arrives), counted as used at finalize, and released whenever the content is deleted; the items reconcile job counts live secrets. Anonymous secrets share `secrets.anonymous_max_total_bytes` of live ciphertext and `secrets.anonymous_per_hour` creates.
- **Uploads.** Chunks arrive in order and must have exactly the size the declared plaintext size implies; a resend of a stored chunk must be byte-identical (compared by SHA-256), so a client bug that re-encrypts a chunk is refused rather than stored. Finalize checks every value's length and refuses KDF parameters outside the bounds the readers enforce.
- **No ids in logs.** Every route with a secret id is registered with `web.Redact()`, so the request log has the route pattern, not the path. Near misses that land on a catch-all route (a wrong method, a trailing slash or dot, `%23` for `#`) under `/s/`, `/api/v1/secrets/`, `/x`, `/verify`, `/reset` and `/invite` are logged with their first path segment only. Ids, probes, tokens, tickets and keys are never logged (tested by capturing the log).
- **The reveal page** (`/s/{id}`, and `/s`, the paste box) is the same bytes for every id and reads nothing from the database. It is sent with `Cache-Control: no-store`, `Referrer-Policy: no-referrer`, `X-Robots-Tag: noindex`, `<meta name="google" content="notranslate">`, and the app CSP plus `'wasm-unsafe-eval'` (for Argon2id). A classic script in its head (`fragment.js`, no module, no defer) copies the key and removes it from the address bar before the rest of the page is parsed, so a module that loads slowly or not at all can't leave it there; the token pages (`/verify`, `/reset`, `/invite`, `/x`) do the same. The paste box opens a pasted link in place, so it never becomes a history entry, and refuses links for other servers.
- **Forms.** The composer, the reveal form and the paste box use `method="dialog"`, which never submits outside a `<dialog>`, and their buttons are disabled until the script has attached its handlers: if a module fails to load, or Enter is pressed before it runs, nothing is sent and nothing goes into the address bar. The item forms on the composer at `/` use `method="dialog"` too. Passphrase fields use `autocomplete="one-time-code"` and the opt-out attributes of the common password managers, so neither the browser nor an extension saves or fills them.
- **Rendering.** Text is shown with `textContent`. Files are offered as `application/octet-stream` downloads with a cleaned name: the last path component, no control, format (invisible, bidi), line or paragraph separator characters, nothing Windows refuses, no leading or trailing dots and spaces, at most 200 bytes, never a Windows device name (CON, PRN, AUX, NUL, COM0–9, LPT0–9, COM¹–³, LPT¹–³, CONIN$, CONOUT$, with any extension). The browser and `sp` follow the same table (`internal/e2e/testdata/safename.json`). Previews are limited to PNG, JPEG, GIF, WebP, AVIF, MP4, WebM and audio, through `<img>`, `<video>` and `<audio>` from a `blob:` URL, and only after the recipient clicks Preview; never SVG, PDF or HTML, never an iframe, never navigation to the blob. Nothing is shown or saved until the whole stream has verified.
- **`sp open`** refuses `http://` links unless `--insecure`. On a terminal it shows control characters in the secret in caret notation unless `--raw`, and it drops control characters from the server's error sentences and cuts them to 300 characters. `-o -` decrypts into memory (at most 64 MiB) and never writes plaintext to a temporary file.
- **Rate limits** on info and claim per client address and per secret, on creating per user, and strict ones on anonymous creating per client address. A client address is an IPv4 address or an IPv6 /64 (`clientip.LimitKey`, used by every per-address limit in the app); the exempt list matches full addresses. Anonymous secrets are off by default and limited to text.

### Threat model

| Adversary | Outcome |
|---|---|
| Knows the id only (logs, proxies) | Gets 404s. Can't read metadata, burn the secret or use up attempts. |
| Has the link, no passphrase set | Can open the secret, which uses a view. The recipient then sees "Opened …" instead of the content, and the owner's timeline shows when and from where. |
| Has the link, passphrase set | Can guess online, `secrets.max_failures` times, after which the secret is destroyed. |
| Mail scanner that opens links | Loading the page uses nothing; only Reveal does. Scanners that click buttons can still use a view: use a passphrase for links sent by email. |
| Has a copy of the database and files, no links | Learns sizes, timing and notes, nothing of the content: `K_link` is 256 random bits. |
| Has the database, the files and a link | While the secret is ready: can test passphrases offline at the Argon2id cost (at least 64 MiB and 3 passes). A tombstone keeps no verifier. Burned content is gone unless a copy of the files survived (snapshots, backups, the raw disk): then the link opens it. |
| Controls the server at view time | Browser mode: can serve JavaScript that leaks the key or plaintext. `sp`: can refuse, lie about state or destroy secrets, but can't decrypt or weaken the passphrase cost. Sealed mode: sees everything. |
| The recipient | Can keep what they saw. |

### Stated limits

- Browser E2E trusts the JavaScript the server sends at view time. Only `sp` is E2E against a malicious server.
- The passphrase's Argon2id cost matters only to an attacker who has both the link and the database. Online guessing is bounded by `secrets.max_failures`.
- Sealed mode trusts the running server.
- A recipient can keep what they saw.
- Browser history and sync may still hold the full URL. A browser records the URL in its history database when the navigation commits, before any script on the page runs; `fragment.js` then removes the fragment from the address bar and the session history entry, but not from that record. Recipients who want to avoid it paste the link into the box at `/s` (it is opened in place and never navigated to) or use `sp open`. The link also stays wherever it was sent.
- File sizes are not padded, so the ciphertext size gives away the exact file size.
- The note is plaintext on the server.
- A file download that started before an expiry or the last view keeps working until its ticket expires; only Burn now cuts it off.
- Whether an id exists may show in response times (one database lookup); ids are 128 random bits and never logged.
- Burning a secret unlinks its ciphertext files; it does not overwrite them. Copies that survive the unlink (free blocks on the raw device, copy-on-write filesystems, snapshots, backups) can still be decrypted by anyone who also has the link. Keep `SNAP_SECRETS_DIR` on a dataset without snapshots and out of backups so that burned means gone.
- `snap-server backup` copies the database as it is, including the rows of ready secrets (with their passphrase verifiers) and tombstones. If burned must mean gone against backups too, exclude both `SNAP_SECRETS_DIR` and the database from snapshots and backups, or keep backups no longer than the secrets' expiry.
- Crypto-shredding (encrypting each file under a key the server deletes at burn) is deliberately not implemented: the key would have to live in the database, which is snapshotted and backed up, so a restored backup would bring back both the key and, with the files, the content. It would move the problem rather than solve it.
- A crash between storing a chunk and recording it can leave one orphaned ciphertext file in `SNAP_SECRETS_DIR`; it holds no plaintext and no key.

## Items and API keys

- **Keys on public routes.** Share pages, raw URLs, unlocks and album pages treat an API key as the item's owner only if it holds `items:read`; any other key (ShareX's `items:write`, `account:read`, `secrets:*`) is a visitor there, so a key never reads more through them than its scopes allow through the API. `GET /` treats every key as signed out.
- **Write without read.** A key without the read scope for an item edits only the items it created (`items.created_by_key`); other items answer `404` to `PATCH`, tag and album changes, so a leaked ShareX key cannot list, read back, unprotect or schedule the deletion of your other items. Adding items to an album needs `items:read`.
- **Expiry caps.** A key with a max expiry cannot use `expiry_action` `private`, and its visibility changes re-apply the cap.
- **Ids.** Item ids are `AUTOINCREMENT` and never reused. Visitor events flushed after their item is gone are dropped, the item's batched counts are discarded on delete, and an hourly sweep removes stats rows of deleted items. Signed raw URLs and unlock cookies are bound to a random per-item value as well as the id.
- **Names.** Freed names stay with their owner for `items.slug_cooldown_days`, so shared links cannot be taken over. Well-known root files (domain verification, `ads.txt`, `security.txt`, sitemaps, cross-domain policies) are reserved. Random names use lowercase letters and digits, at least 10 (about 52 bits), because lookups ignore case. Misses on content URLs are limited to 30 a minute per address and 1200 a minute in total.
- **Raw origin.** With `SNAP_RAW_URL`, that host serves only raw content URLs, anonymously (cookies and `Authorization` are dropped, no cookie is ever set); everything else redirects to the app.
- **Item passwords** are at least 8 characters. Unlocks are limited per address and item, per address, and to 20 wrong passwords per item per hour from all addresses; they hash on two dedicated slots of the shared Argon2id limit.
- **Admin totals.** An admin sees another user's item stats (totals only, without its name or link) only from `SNAP_ADMIN_CIDRS` when that is set.

## Accounts, keys and requests

What protects accounts, and what ends when an account may be in the wrong hands. Details in [accounts.md](accounts.md).

- **Request bodies.** Every route reads at most 1 MiB of body, within 30 seconds, and refuses `multipart/form-data` with 415, unless it is registered with `web.LargeBody()` (uploads, upload chunks, pastes, secret chunks, sealed secrets), which set their own limits and stream instead of parsing forms. Without this, any form handler would spool an anonymous multipart upload of any size to `/tmp` before its rate limit ran.
- **Password guessing.** Each sign-in takes a token from the per-name budget (5 failures per 15 minutes) before the password is hashed, and a successful one gives it back. Parallel guesses get at most what is left of the budget, also from an exempt address; parallel correct sign-ins wait for each other instead of locking the account. Username and email address have separate budgets, so a locked username doesn't reveal its address.
- **Recovery ends access.** A password reset ends every session and revokes every API key. So does **Sign out everywhere**. A password change ends every session (this browser gets a new session id, so a stolen copy of its cookie stops working) and, by default, revokes the keys. Disabling an account ends its sessions and revokes its keys for good.
- **Re-authentication.** Changing the email address, setting or changing the password, linking and unlinking SSO, adding or removing a passkey or the authenticator app, making new recovery codes and creating an API key with the `admin` scope need the current password, or for accounts without one a sign-in from the last 10 minutes. An account with a second factor must also give one in the same request: a code, or a passkey with user verification; without a password, the passkey can stand in for the recent sign-in.
- **Second factors.** A user with a passkey or an authenticator app gets a browser session only after the password and a factor, from a passkey with user verification, or from SSO. This covers password sign-in, password resets and the session a password change starts.
  - **Passkeys resist phishing; codes don't.** A passkey belongs to the host of `SNAP_PUBLIC_URL` (the RP ID): the browser won't use it on another site, and the server accepts an answer only for that RP ID and from the public origin, never from `SNAP_RAW_URL`. A page that relays the sign-in gets nothing it can use. An authenticator-app or recovery code typed into such a page can be relayed within its validity. Moving the site to another hostname makes every passkey unusable.
  - **Sign-in with a passkey alone** needs user verification (a PIN, fingerprint or face) in that assertion, so the passkey counts as two factors. After a password, presence is enough, so PIN-less security keys work as the second step. It is only offered while `auth.password_login` is on. Starting one is limited per address (30 a minute, apart from the password budget, because the sign-in page starts one for autofill); a failed one spends the address's sign-in budget.
  - **Ceremonies.** Each passkey ceremony and second step keeps its state on the server for 5 minutes under the hash of a token in a cookie (`__Host-snap_auth_<kind>`, `SameSite=Strict`), so only the browser that began it can finish it. A passkey sign-in, registration or proof uses its challenge up before checking the answer, and the second step when it succeeds, so an answer works once. The signature counter must go up (or stay at 0, which synced passkeys report): a counter that goes back means a copied key, so it is refused and audited as `auth.passkey_clone`. The backup-eligible flag must match the one registered.
  - **The second step** allows 5 failures per password sign-in. Wrong codes also spend the account's budget; a failed passkey doesn't, so someone with only the password can't lock the owner out of their passkey.
  - **Password resets** of an account with a factor change nothing until the factor is given: the link only opens the second step, and the token, the password, the sessions and the keys change together after it.
  - **Recovery codes**: 10, each 16 characters of Crockford base32 (80 bits), made with the first factor and shown once. Only SHA-256 of a label, the user id and the code is stored; each works once, also under parallel requests. Using one sends a security email with the number left.
  - **Authenticator-app secrets** are sealed with a key derived from `SNAP_SECRET_KEY` and bound to the user id. After a key rotation, a secret that opens only with `SNAP_SECRET_KEY_PREVIOUS` is sealed again with the new key at its next use; keep the previous key until users have signed in. A secret that opens with neither isn't offered: the user signs in with a passkey or a recovery code. A code works once (the last time step used is kept).
  - **SSO** sign-ins skip snap's second step; the identity provider is in charge of 2FA there.
  - **API keys** are not covered: they can't make browser sessions or set-password links, and existing keys keep working when a factor is added. Revoke them in Settings if one may have leaked.
  - **The first factor** ends the user's other sessions. While `auth.require_2fa` covers a user, their last factor can't be removed, and a password sign-in without one can only set one up.
  - **Admin reset** (Admin › Users, or `snap-server admin reset-2fa`) removes the user's passkeys, authenticator app, recovery codes and sign-ins in progress, ends their sessions, is audited as `user.mfa_reset` and emails the user. In the admin pages the admin confirms it's them, with their own factor, and can't reset their own account there. The user's API keys stay.
  - **Security emails** go to the verified address for every factor added or removed, a recovery code used, new recovery codes, an admin reset and a password reset, whatever the notification settings. Their text is fixed and links only to the security settings. The in-app inbox isn't used, because whoever holds a stolen session could read and clear it.
- **Email addresses.** The profile form answers alike whether or not an address has an account, and spends the mail limit before it looks. Opening a verification link changes nothing until **Confirm** is clicked, so a mail scanner can't confirm an address for an account someone else registered. SSO links by email (`SNAP_OIDC_LINK_BY_EMAIL`) only to accounts without a password or a second factor.
- **Admin keys.** A key with the `admin` scope can use the admin API and pages but never gets a set-password or invite link (they are only emailed when a key asks, and refused with 403 when they can't be), and can't give the admin role. Otherwise a key could turn itself into a browser admin session, which can do what keys can't (create keys, change account settings).
- **SSO roles.** The role mapping never demotes the last active admin. A demotion, or a refused sign-in after the provider removed someone from the allowed groups, ends their sessions and revokes their keys with the `admin` scope. An SSO-linked admin's session has admin rights for 24 hours after an SSO sign-in. Keys are not re-checked against the provider; disable the account to end them at once.
- **Invites** stop working when the admin who made them is disabled, demoted or deleted.
- **Cross-site requests.** Every state-changing route refuses cross-site requests made with the session cookie. Linking an SSO account needs a POST (a GET to `/auth/oidc/start?link=1` only goes to Settings).
- **"Send link" mail** has a fixed subject and body: the sender's username, the site name, each item's kind and its link. No title, file name or link target, so the server's address can't relay a phishing text.
- **Notifications** are not logged and not queued as email when SMTP is not set up (the fallback sender logs nothing about a message), and finished delivery jobs, which hold the visitor's address and client and the secret's note, are deleted within 10 minutes. Item opens and secret reveals have separate hourly caps. A saved ntfy token is dropped when the server changes.
- **Logs.** Near misses of redacted routes under `/s/`, `/api/v1/secrets/`, `/dashboard/secrets/`, `/api/v1/zip/`, `/unlock/`, `/x`, `/verify`, `/reset` and `/invite` are logged with their first path segment only.
