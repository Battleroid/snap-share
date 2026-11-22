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

Passphrase rules:

- The passphrase MUST be non-empty and well-formed Unicode (valid UTF-8 in Go, no lone surrogates in JS). It is normalised to NFC, not NFKC, before UTF-8 encoding, so composed and decomposed input match but compatibility characters (`ﬁ` and `fi`) stay distinct.
- Defaults: `m` = 65536 KiB (64 MiB), `t` = 3, `p` = 1.
- Readers MUST refuse parameters outside `m` ∈ [19456, 262144], `t` ∈ [1, 10], `p` = 1 before running Argon2id. The parameters come from the server; without the check a malicious server could lower the cost to make offline guessing cheaper, or make the browser allocate gigabytes.

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

It never receives `K_link`, the passphrase, `K_enc` or `K_meta`. With the database and the link, an attacker can test passphrases offline at the Argon2id cost. Without the link, the stored values do not help: `K_link` is 256 random bits. In the browser, this holds only as far as the JavaScript the server sends can be trusted; in sealed mode the server generates `K_link` and sees the plaintext while encrypting.

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
| `secrets` row | id, owner, kind, mode, state; `S`, KDF parameters (so whether there is a passphrase), SHA-256(`K_probe`), SHA-256(`K_auth`), `K_commit`; ciphertext size and chunk count; views left and maximum, failed attempts and maximum; expiry; the owner's note (plaintext, owner only); the notify flag; first-open time, burn time and reason. While uploading only: SHA-256 of the upload token and the creator's IP address (for the open-upload cap), both cleared at finalize. |
| `secret_tickets` | SHA-256 of each download ticket, its secret and expiry. |
| `secret_blobs` | the blob-store key, size and SHA-256 of each ciphertext file (sealed metadata and content chunks). |
| `secret_events` | for the owner's timeline: time, kind (link opened, revealed, wrong passphrase, burned), IP address, User-Agent and its summary. Deleted after the stats retention period, or with the secret. Anonymous secrets and the owner's own requests record nothing. |
| `SNAP_SECRETS_DIR` | the sealed metadata (1052 bytes) and the content ciphertext, one file per 1 MiB chunk. |

The server never stores `K_link`, the passphrase, `K_enc`, `K_meta` or any plaintext. No ciphertext is stored in the database.

### What the server sees

- The id on every request; `K_probe` on info and claim; `K_auth` on claim; the upload token while uploading; the ticket while downloading.
- The ciphertext size: padded for text (a power of two, at least 1 KiB), exact for files. File names and types are inside the encrypted metadata.
- When, how often and from which IP address and browser a secret is opened.
- In sealed mode, the plaintext and the key during the upload request.

### What the server enforces

- **Link holders only.** Info and claim need the probe. A wrong or missing probe, an unknown or malformed id, and an unfinished upload all get the same 404 body, and none of them count as a failed attempt. Someone who learns only the id (from a proxy log, say) can neither read the metadata nor burn the secret.
- **One view per claim, atomically.** A claim runs in one `BEGIN IMMEDIATE` transaction that re-checks the probe, state and expiry, then either counts a failed attempt (burning the secret at `secrets.max_failures`, default 10, and notifying the owner) or takes a view. A wrong passphrase never uses a view. 100 parallel claims on a 3-view secret give exactly 3 successes (tested).
- **Expiry on every request.** A ready secret past its expiry is treated as expired before the sweeper notices.
- **Tickets.** A file claim returns a 256-bit ticket, stored hashed, bound to that secret, sent in the `X-Snap-Ticket` header (never in a URL), valid for max(15 minutes, the size at 1 Mbit/s).
- **Deletion.** A text secret's ciphertext is deleted right after the response carrying its last view is flushed. A file's ciphertext is deleted when the client reports the last chunk verified (`done`) and no other live ticket exists, otherwise by the sweeper once the last ticket expires; the sweeper never deletes a file a live ticket needs. Burn now (owner or admin) ends live tickets and deletes the files at once. Unfinished uploads are deleted after an hour. Files are tracked in `secret_blobs` without a foreign key, so files of rows deleted any other way (a user deleted with their secrets) are still found and deleted. Tombstones are kept 30 days, then deleted with their events.
- **Uploads.** Chunks arrive in order and must have exactly the size the declared plaintext size implies; a resend of a stored chunk must be byte-identical (compared by SHA-256), so a client bug that re-encrypts a chunk is refused rather than stored. Finalize checks every value's length and refuses KDF parameters outside the bounds the readers enforce.
- **No ids in logs.** Every route with a secret id is registered with `web.Redact()`, so the request log has the route pattern, not the path; ids, probes, tokens, tickets and keys are never logged (tested by capturing the log).
- **The reveal page** (`/s/{id}`) is the same bytes for every id and reads nothing from the database. It is sent with `Cache-Control: no-store`, `Referrer-Policy: no-referrer`, `X-Robots-Tag: noindex`, `<meta name="google" content="notranslate">`, and the app CSP plus `'wasm-unsafe-eval'` (for Argon2id). Its script removes the key from the address bar before anything else.
- **Rendering.** Text is shown with `textContent`. Files are offered as `application/octet-stream` downloads with a cleaned name (no paths, control, invisible or bidi characters, no leading dots). Inline previews are limited to PNG, JPEG, GIF, WebP, AVIF, MP4, WebM and audio, through `<img>`, `<video>` and `<audio>` from a `blob:` URL; never SVG, PDF or HTML, never an iframe, never navigation to the blob. Nothing is shown or saved until the whole stream has verified.
- **Rate limits** on info and claim per IP and per secret, on creating per user, and strict ones on anonymous creating per IP. Anonymous secrets are off by default and limited to text.

### Threat model

| Adversary | Outcome |
|---|---|
| Knows the id only (logs, proxies) | Gets 404s. Can't read metadata, burn the secret or use up attempts. |
| Has the link, no passphrase set | Can open the secret, which uses a view. The recipient then sees "Opened …" instead of the content, and the owner's timeline shows when and from where. |
| Has the link, passphrase set | Can guess online, `secrets.max_failures` times, after which the secret is destroyed. |
| Mail scanner that opens links | Loading the page uses nothing; only Reveal does. Scanners that click buttons can still use a view: use a passphrase for links sent by email. |
| Has a copy of the database and files, no links | Learns sizes, timing and notes, nothing of the content: `K_link` is 256 random bits. |
| Has the database, the files and a link | Can test passphrases offline at the Argon2id cost (64 MiB, 3 passes by default). |
| Controls the server at view time | Browser mode: can serve JavaScript that leaks the key or plaintext. `sp`: can refuse, lie about state or destroy secrets, but can't decrypt or weaken the passphrase cost. Sealed mode: sees everything. |
| The recipient | Can keep what they saw. |

### Stated limits

- Browser E2E trusts the JavaScript the server sends at view time. Only `sp` is E2E against a malicious server.
- The passphrase's Argon2id cost matters only to an attacker who has both the link and the database. Online guessing is bounded by `secrets.max_failures`.
- Sealed mode trusts the running server.
- A recipient can keep what they saw.
- Browser history and sync may still hold the full URL: the page removes the fragment with `history.replaceState` as soon as it runs, but a URL typed or pasted into the address bar can be recorded first, and the link stays wherever it was sent.
- File sizes are not padded, so the ciphertext size gives away the exact file size.
- The note is plaintext on the server.
- A file download that started before an expiry or the last view keeps working until its ticket expires; only Burn now cuts it off.
- Whether an id exists may show in response times (one database lookup); ids are 128 random bits and never logged.
- Deleted files can survive in filesystem snapshots: put `SNAP_SECRETS_DIR` on a dataset without snapshots so burned really means gone. SQLite runs with `secure_delete`.
- A crash between storing a chunk and recording it can leave one orphaned ciphertext file in `SNAP_SECRETS_DIR`; it holds no plaintext and no key.
