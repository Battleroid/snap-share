// snap one-time-secret envelope v1 for browsers.
//
// Spec: docs/security.md, "E2E envelope v1". This module mirrors the Go
// package internal/e2e; both are tested against
// internal/e2e/testdata/vectors.json. It uses WebCrypto for HKDF, SHA-256
// and AES-GCM, and the vendored hash-wasm Argon2id (loaded on first use of a
// passphrase; needs CSP 'wasm-unsafe-eval').
//
// Byte values are Uint8Array. Functions that take an AES key (encryptChunk,
// decryptChunk, encryptBlob, decryptChunks, sealMeta, openMeta) also accept
// a CryptoKey from importAesKey.

const utf8 = new TextEncoder();

export const LABEL = "snap-e2e-v1";
export const ID_SIZE = 16;
export const KEY_SIZE = 32;
export const SALT_SIZE = 16;
export const CHUNK_SIZE = 1 << 20;
export const TAG_SIZE = 16;
export const NONCE_SIZE = 12;
export const META_VERSION = 1;
export const META_SIZE = 1024;
export const SEALED_META_SIZE = NONCE_SIZE + META_SIZE + TAG_SIZE;
export const MAX_META_JSON = META_SIZE - 2;
export const MIN_TEXT_PAD = 1024;
export const MAX_SIZE = Number.MAX_SAFE_INTEGER;
export const KIND_TEXT = "text";
export const KIND_FILE = "file";
// The floor is the creators' default (defaultKdf): nothing legitimate asks
// for less, so a server that does is trying to weaken the passphrase cost.
export const KDF_BOUNDS = Object.freeze({ minM: 65536, maxM: 262144, minT: 3, maxT: 10, p: 1 });

const FULL_CHUNK = CHUNK_SIZE + TAG_SIZE;

// E2EError.code is one of: unsupported, kdf, passphrase, commit, encoding,
// link, decrypt, chunk-size, too-large, meta.
export class E2EError extends Error {
  constructor(code, message) {
    super(`e2e: ${message}`);
    this.name = "E2EError";
    this.code = code;
  }
}

// WebCrypto exists only in secure contexts (https or localhost).
function subtle() {
  const s = globalThis.crypto?.subtle;
  if (!s) throw new E2EError("unsupported", "WebCrypto is unavailable; secrets need https or localhost");
  return s;
}

// supported reports whether this page can encrypt and decrypt secrets.
export function supported() {
  return Boolean(globalThis.crypto?.subtle);
}

// ---- bytes ----

function bytes(x, what = "value") {
  if (x instanceof Uint8Array) return x;
  if (x instanceof ArrayBuffer) return new Uint8Array(x);
  if (ArrayBuffer.isView(x)) return new Uint8Array(x.buffer, x.byteOffset, x.byteLength);
  throw new TypeError(`e2e: ${what} must be bytes`);
}

function fixed(x, n, what) {
  const b = bytes(x, what);
  if (b.length !== n) throw new TypeError(`e2e: ${what} must be ${n} bytes`);
  return b;
}

function concat(...parts) {
  const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
  let off = 0;
  for (const p of parts) {
    out.set(p, off);
    off += p.length;
  }
  return out;
}

// equalBytes compares without an early exit on the first difference.
export function equalBytes(a, b) {
  a = bytes(a);
  b = bytes(b);
  if (a.length !== b.length) return false;
  let d = 0;
  for (let i = 0; i < a.length; i++) d |= a[i] ^ b[i];
  return d === 0;
}

// ---- base64url (unpadded, canonical) ----

const B64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";

export function encode(b) {
  b = bytes(b);
  let out = "";
  let i = 0;
  for (; i + 2 < b.length; i += 3) {
    const n = (b[i] << 16) | (b[i + 1] << 8) | b[i + 2];
    out += B64[n >> 18] + B64[(n >> 12) & 63] + B64[(n >> 6) & 63] + B64[n & 63];
  }
  if (b.length - i === 1) {
    const n = b[i] << 16;
    out += B64[n >> 18] + B64[(n >> 12) & 63];
  } else if (b.length - i === 2) {
    const n = (b[i] << 16) | (b[i + 1] << 8);
    out += B64[n >> 18] + B64[(n >> 12) & 63] + B64[(n >> 6) & 63];
  }
  return out;
}

function decodeFixed(s, n) {
  const bad = () => new E2EError("encoding", "invalid base64url value");
  if (typeof s !== "string" || s.length !== Math.ceil((n * 4) / 3)) throw bad();
  const out = new Uint8Array(n);
  let acc = 0;
  let nbits = 0;
  let j = 0;
  for (let i = 0; i < s.length; i++) {
    const v = B64.indexOf(s[i]);
    if (v < 0) throw bad();
    acc = (acc << 6) | v;
    nbits += 6;
    if (nbits >= 8) {
      nbits -= 8;
      out[j++] = (acc >> nbits) & 0xff;
    }
    acc &= (1 << nbits) - 1;
  }
  if (acc !== 0) throw bad(); // non-zero trailing bits: not the canonical encoding
  return out;
}

// decode16 decodes a 22-character value (id, salt).
export function decode16(s) {
  return decodeFixed(s, 16);
}

// decode32 decodes a 43-character value (K_link, derived keys, probe, commit).
export function decode32(s) {
  return decodeFixed(s, 32);
}

// ---- link ----

// newLinkKey returns a fresh K_link. Use a new one for every create attempt.
export function newLinkKey() {
  return globalThis.crypto.getRandomValues(new Uint8Array(KEY_SIZE));
}

// newSalt returns a fresh envelope salt S.
export function newSalt() {
  return globalThis.crypto.getRandomValues(new Uint8Array(SALT_SIZE));
}

// formatLink returns {base}/s/{id}#{K_link}; trailing slashes on base are dropped.
export function formatLink(base, id, key) {
  return (
    String(base).replace(/\/+$/, "") +
    "/s/" +
    encode(fixed(id, ID_SIZE, "id")) +
    "#" +
    encode(fixed(key, KEY_SIZE, "key"))
  );
}

// parseLink splits a link into { base, id, key }. Errors never include the key.
export function parseLink(s) {
  s = String(s).trim();
  const h = s.indexOf("#");
  if (h < 0) throw new E2EError("link", "invalid secret link: no key fragment");
  const path = s.slice(0, h);
  const i = path.lastIndexOf("/s/");
  if (i < 0) throw new E2EError("link", "invalid secret link: no /s/ path");
  let id;
  let key;
  try {
    id = decode16(path.slice(i + 3));
  } catch {
    throw new E2EError("link", "invalid secret link: bad id");
  }
  try {
    key = decode32(s.slice(h + 1));
  } catch {
    throw new E2EError("link", "invalid secret link: bad key");
  }
  return { base: path.slice(0, i), id, key };
}

// ---- key derivation ----

export function defaultKdf() {
  return { m: 65536, t: 3, p: 1 };
}

// validateKdf throws unless params is { m: 65536..262144 KiB, t: 3..10, p: 1 }.
export function validateKdf(params) {
  const { m, t, p } = params ?? {};
  const b = KDF_BOUNDS;
  if (!Number.isInteger(m) || m < b.minM || m > b.maxM) {
    throw new E2EError("kdf", `kdf parameters out of bounds: m=${m} KiB, want ${b.minM}..${b.maxM}`);
  }
  if (!Number.isInteger(t) || t < b.minT || t > b.maxT) {
    throw new E2EError("kdf", `kdf parameters out of bounds: t=${t}, want ${b.minT}..${b.maxT}`);
  }
  if (p !== b.p) throw new E2EError("kdf", `kdf parameters out of bounds: p=${p}, want ${b.p}`);
}

function info(label, id) {
  const l = utf8.encode(label);
  return concat(Uint8Array.of(l.length), l, id);
}

async function hkdfKey(ikm) {
  return subtle().importKey("raw", ikm, "HKDF", false, ["deriveBits"]);
}

async function hkdfExpand(ikmKey, salt, infoBytes) {
  const bits = await subtle().deriveBits({ name: "HKDF", hash: "SHA-256", salt, info: infoBytes }, ikmKey, KEY_SIZE * 8);
  return new Uint8Array(bits);
}

// probeKey derives K_probe = HKDF-SHA256(K_link, salt "snap-e2e-v1", lp("probe") || id).
export async function probeKey(key, id) {
  key = fixed(key, KEY_SIZE, "key");
  id = fixed(id, ID_SIZE, "id");
  return hkdfExpand(await hkdfKey(key), utf8.encode(LABEL), info("probe", id));
}

const ARGON2_URL = new URL("../vendor/hash-wasm/argon2.umd.min.js", import.meta.url).href;
let argon2Loading = null;

// The vendored file is UMD: loaded as a module in a browser it sets
// globalThis.hashwasm; Node loads it as CommonJS and returns it as default.
function loadArgon2id() {
  argon2Loading ??= import(ARGON2_URL).then(
    (mod) => {
      const fn = mod.default?.argon2id ?? globalThis.hashwasm?.argon2id;
      if (typeof fn !== "function") throw new E2EError("kdf", "argon2id unavailable");
      return fn;
    },
    (err) => {
      argon2Loading = null;
      throw err;
    },
  );
  return argon2Loading;
}

// passphraseKey derives K_pw = Argon2id(UTF-8(NFC(passphrase)), salt, t, m, p, 32).
export async function passphraseKey(passphrase, salt, params) {
  validateKdf(params);
  salt = fixed(salt, SALT_SIZE, "salt");
  if (typeof passphrase !== "string" || passphrase.length === 0) {
    throw new E2EError("passphrase", "invalid passphrase: empty");
  }
  if (!passphrase.isWellFormed()) throw new E2EError("passphrase", "invalid passphrase: lone surrogate");
  const argon2id = await loadArgon2id();
  return argon2id({
    password: utf8.encode(passphrase.normalize("NFC")),
    salt,
    parallelism: params.p,
    iterations: params.t,
    memorySize: params.m,
    hashLength: KEY_SIZE,
    outputType: "binary",
  });
}

// deriveKeys returns { auth, enc, meta, commit }:
//   PRK = HKDF-Extract(salt "snap-e2e-v1" || S, IKM K_link || K_pw)
//   K_x = HKDF-Expand(PRK, lp(x) || id, 32)
// passphrase null/undefined means none (params ignored); otherwise a
// non-empty string and params must pass validateKdf.
export async function deriveKeys(key, id, salt, passphrase = null, params = null) {
  key = fixed(key, KEY_SIZE, "key");
  id = fixed(id, ID_SIZE, "id");
  salt = fixed(salt, SALT_SIZE, "salt");
  let ikm = key;
  if (passphrase !== null && passphrase !== undefined) {
    const kpw = await passphraseKey(passphrase, salt, params);
    ikm = concat(key, kpw);
    kpw.fill(0);
  }
  const base = await hkdfKey(ikm);
  if (ikm !== key) ikm.fill(0);
  const extractSalt = concat(utf8.encode(LABEL), salt);
  const [auth, enc, meta, commit] = await Promise.all(
    ["auth", "enc", "meta", "commit"].map((label) => hkdfExpand(base, extractSalt, info(label, id))),
  );
  return { auth, enc, meta, commit };
}

// checkCommit throws unless commit equals keys.commit. Call it before
// decrypting anything.
export function checkCommit(keys, commit) {
  if (!equalBytes(keys.commit, commit)) throw new E2EError("commit", "key commitment mismatch");
}

// hash returns SHA-256(b).
export async function hash(b) {
  return new Uint8Array(await subtle().digest("SHA-256", bytes(b)));
}

// ---- content: STREAM over AES-256-GCM ----

// numChunks returns the chunk count for n plaintext bytes (at least 1), or -1.
export function numChunks(n) {
  if (!Number.isSafeInteger(n) || n < 0) return -1;
  return n === 0 ? 1 : Math.floor((n - 1) / CHUNK_SIZE) + 1;
}

// ciphertextSize returns n + 16 per chunk, or -1 for invalid input.
export function ciphertextSize(n) {
  const c = numChunks(n);
  if (c < 0 || n > MAX_SIZE - c * TAG_SIZE) return -1;
  return n + c * TAG_SIZE;
}

// importAesKey imports a raw 32-byte key for AES-GCM.
export async function importAesKey(raw) {
  return subtle().importKey("raw", fixed(raw, KEY_SIZE, "key"), "AES-GCM", false, ["encrypt", "decrypt"]);
}

function aesKey(k) {
  if (typeof CryptoKey !== "undefined" && k instanceof CryptoKey) return k;
  return importAesKey(k);
}

// chunkNonce is the 11-byte big-endian counter followed by 0x01 on the last
// chunk, else 0x00.
function chunkNonce(counter, last) {
  if (!Number.isSafeInteger(counter) || counter < 0) {
    throw new E2EError("too-large", `invalid chunk counter ${counter}`);
  }
  const n = new Uint8Array(NONCE_SIZE);
  const dv = new DataView(n.buffer);
  dv.setUint32(3, Math.floor(counter / 2 ** 32));
  dv.setUint32(7, counter >>> 0);
  n[11] = last ? 1 : 0;
  return n;
}

// validChunkLen: non-final chunks are full, the final chunk holds
// 0..CHUNK_SIZE bytes and is empty only when it is the only chunk.
function validChunkLen(n, counter, last) {
  if (!last) return n === CHUNK_SIZE;
  return n <= CHUNK_SIZE && (n > 0 || counter === 0);
}

// encryptChunk encrypts one chunk. A non-final chunk must be exactly
// CHUNK_SIZE bytes, the final one 1..CHUNK_SIZE (or empty as the only
// chunk). Never encrypt different plaintext under the same key and counter:
// retries resend the first ciphertext.
export async function encryptChunk(kEnc, counter, last, pt) {
  pt = bytes(pt, "plaintext");
  if (!validChunkLen(pt.length, counter, last)) {
    throw new E2EError("chunk-size", `invalid chunk size: ${pt.length} bytes, last=${!!last}`);
  }
  const iv = chunkNonce(counter, last);
  const key = await aesKey(kEnc);
  return new Uint8Array(await subtle().encrypt({ name: "AES-GCM", iv, tagLength: 128 }, key, pt));
}

// decryptChunk decrypts and authenticates one chunk, with the same length
// rules as encryptChunk applied to the ciphertext minus its tag. Failures
// throw E2EError code "decrypt".
export async function decryptChunk(kEnc, counter, last, ct) {
  ct = bytes(ct, "ciphertext");
  if (ct.length < TAG_SIZE || !validChunkLen(ct.length - TAG_SIZE, counter, last)) {
    throw new E2EError("decrypt", `decryption failed: chunk ${counter}: bad length`);
  }
  const iv = chunkNonce(counter, last);
  const key = await aesKey(kEnc);
  try {
    return new Uint8Array(await subtle().decrypt({ name: "AES-GCM", iv, tagLength: 128 }, key, ct));
  } catch {
    throw new E2EError("decrypt", `decryption failed: chunk ${counter}`);
  }
}

// encryptBlob reads a Blob (or bytes) slice by slice and yields each
// ciphertext chunk once, in order; the last yielded chunk is the final one.
// Keep each chunk until the server has accepted it and resend those same
// bytes on retry: re-encrypting would reuse a nonce.
export async function* encryptBlob(kEnc, blob) {
  if (!(blob instanceof Blob)) blob = new Blob([bytes(blob, "blob")]);
  const key = await aesKey(kEnc);
  const n = numChunks(blob.size);
  for (let i = 0; i < n; i++) {
    const pt = new Uint8Array(await blob.slice(i * CHUNK_SIZE, (i + 1) * CHUNK_SIZE).arrayBuffer());
    yield await encryptChunk(key, i, i === n - 1, pt);
  }
}

class ByteQueue {
  constructor() {
    this.parts = [];
    this.length = 0;
  }
  push(b) {
    if (b.length > 0) {
      this.parts.push(b.slice());
      this.length += b.length;
    }
  }
  take(n) {
    const out = new Uint8Array(n);
    let off = 0;
    while (off < n) {
      const p = this.parts[0];
      const k = Math.min(p.length, n - off);
      out.set(p.subarray(0, k), off);
      off += k;
      if (k === p.length) this.parts.shift();
      else this.parts[0] = p.subarray(k);
    }
    this.length -= n;
    return out;
  }
}

async function* pieces(source) {
  if (source instanceof Blob) source = source.stream();
  if (source instanceof ArrayBuffer || ArrayBuffer.isView(source)) {
    yield bytes(source);
    return;
  }
  if (source?.[Symbol.asyncIterator] || source?.[Symbol.iterator]) {
    for await (const p of source) yield bytes(p, "ciphertext");
    return;
  }
  if (typeof source?.getReader === "function") {
    const reader = source.getReader();
    try {
      for (;;) {
        const { done, value } = await reader.read();
        if (done) return;
        yield bytes(value, "ciphertext");
      }
    } finally {
      reader.releaseLock();
    }
  }
  throw new TypeError("e2e: ciphertext source must be bytes, a Blob, a stream or an iterable");
}

// decryptChunks decrypts a STREAM and yields plaintext chunks. source is
// bytes, a Blob, a ReadableStream, or an (async) iterable of byte pieces of
// any size (e.g. a fetch body); pieces are re-framed into chunks. A chunk is
// final when the input ends after it. Each yielded chunk has been
// authenticated, but truncation is only detected at the end: treat the
// output as untrusted until the generator finishes without throwing.
// Integrity failures throw E2EError code "decrypt".
export async function* decryptChunks(kEnc, source) {
  const key = await aesKey(kEnc);
  const q = new ByteQueue();
  let counter = 0;
  for await (const piece of pieces(source)) {
    q.push(piece);
    while (q.length > FULL_CHUNK) {
      if (counter === Number.MAX_SAFE_INTEGER) throw new E2EError("too-large", "stream too large");
      yield await decryptChunk(key, counter, false, q.take(FULL_CHUNK));
      counter++;
    }
  }
  if (q.length < TAG_SIZE) throw new E2EError("decrypt", `decryption failed: chunk ${counter}: truncated`);
  yield await decryptChunk(key, counter, true, q.take(q.length));
}

// ---- text padding ----

// paddedTextLen is the next power of two >= max(n, 1024).
export function paddedTextLen(n) {
  let p = MIN_TEXT_PAD;
  while (p < n) p *= 2;
  return p;
}

// padText returns a copy of b zero-padded to paddedTextLen(b.length). Store
// b.length as the metadata size.
export function padText(b) {
  b = bytes(b);
  const out = new Uint8Array(paddedTextLen(b.length));
  out.set(b);
  return out;
}

// unpadText returns the first size bytes of padded text.
export function unpadText(b, size) {
  b = bytes(b);
  if (!Number.isSafeInteger(size) || size < 0 || size > b.length) {
    throw new E2EError("meta", `invalid metadata: size ${size} exceeds content`);
  }
  return b.subarray(0, size);
}

// ---- metadata ----

function optString(x, what) {
  if (x === undefined || x === null) return "";
  if (typeof x !== "string") throw new E2EError("meta", `invalid metadata: ${what} must be a string`);
  return x.toWellFormed();
}

// encodeMeta returns { plaintext, meta }: the 1024-byte record
// (uint16 BE length || JSON || zeros) and the metadata as it will read back.
// meta.v must be undefined, 0 or 1. The name is cut to the longest prefix
// (whole code points) that fits; any other overflow throws.
function encodeMeta(m) {
  if (m.v !== undefined && m.v !== 0 && m.v !== META_VERSION) {
    throw new E2EError("meta", `invalid metadata: version ${m.v}`);
  }
  if (m.kind !== KIND_TEXT && m.kind !== KIND_FILE) throw new E2EError("meta", `invalid metadata: kind ${m.kind}`);
  if (!Number.isSafeInteger(m.size) || m.size < 0) throw new E2EError("meta", `invalid metadata: size ${m.size}`);
  const name = optString(m.name, "name");
  const stored = { v: META_VERSION, kind: m.kind, name: "", type: optString(m.type, "type"), size: m.size };
  let budget = MAX_META_JSON - utf8.encode(JSON.stringify(stored)).length;
  if (budget < 0) throw new E2EError("meta", "invalid metadata: type too long");
  let cut = "";
  for (const ch of name) {
    const w = utf8.encode(JSON.stringify(ch)).length - 2;
    if (w > budget) break;
    budget -= w;
    cut += ch;
  }
  stored.name = cut;
  const js = utf8.encode(JSON.stringify(stored));
  const plaintext = new Uint8Array(META_SIZE);
  plaintext[0] = js.length >> 8;
  plaintext[1] = js.length & 0xff;
  plaintext.set(js, 2);
  return { plaintext, meta: stored };
}

async function sealMetaRaw(kMeta, plaintext, nonce) {
  const key = await aesKey(kMeta);
  const ct = new Uint8Array(await subtle().encrypt({ name: "AES-GCM", iv: nonce, tagLength: 128 }, key, plaintext));
  return concat(nonce, ct);
}

async function sealMetaWithNonce(kMeta, meta, nonce) {
  return sealMetaRaw(kMeta, encodeMeta(meta).plaintext, fixed(nonce, NONCE_SIZE, "nonce"));
}

// sealMeta seals { kind, name, type, size } under K_meta with a random
// nonce: nonce || AES-GCM(plaintext), SEALED_META_SIZE bytes.
export async function sealMeta(kMeta, meta) {
  return sealMetaWithNonce(kMeta, meta, globalThis.crypto.getRandomValues(new Uint8Array(NONCE_SIZE)));
}

function parseMeta(pt) {
  const bad = (why) => new E2EError("meta", `invalid metadata: ${why}`);
  const n = (pt[0] << 8) | pt[1];
  if (n === 0 || n > MAX_META_JSON) throw bad(`json length ${n}`);
  for (let i = 2 + n; i < pt.length; i++) if (pt[i] !== 0) throw bad("non-zero padding");
  let obj;
  try {
    // ignoreBOM keeps a leading U+FEFF so JSON.parse rejects it, as Go does.
    obj = JSON.parse(new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(pt.subarray(2, 2 + n)));
  } catch {
    throw bad("not UTF-8 JSON");
  }
  if (obj === null || typeof obj !== "object" || Array.isArray(obj)) throw bad("not a JSON object");
  if (obj.v !== META_VERSION) throw bad("unsupported version");
  if (obj.kind !== KIND_TEXT && obj.kind !== KIND_FILE) throw bad("bad kind");
  if (typeof obj.name !== "string") throw bad("bad name");
  if (typeof obj.type !== "string") throw bad("bad type");
  if (!Number.isSafeInteger(obj.size) || obj.size < 0) throw bad("bad size");
  return { v: META_VERSION, kind: obj.kind, name: obj.name, type: obj.type, size: obj.size + 0 };
}

// openMeta decrypts and parses sealed metadata into { v, kind, name, type,
// size }. It throws on a wrong length, a failed tag (code "decrypt") or any
// malformed field (code "meta"). Unknown JSON fields are ignored.
export async function openMeta(kMeta, sealed) {
  sealed = bytes(sealed, "metadata");
  if (sealed.length !== SEALED_META_SIZE) {
    throw new E2EError("meta", `invalid metadata: ${sealed.length} bytes, want ${SEALED_META_SIZE}`);
  }
  const key = await aesKey(kMeta);
  let pt;
  try {
    pt = new Uint8Array(
      await subtle().decrypt({ name: "AES-GCM", iv: sealed.subarray(0, NONCE_SIZE), tagLength: 128 }, key, sealed.subarray(NONCE_SIZE)),
    );
  } catch {
    throw new E2EError("decrypt", "decryption failed: metadata");
  }
  return parseMeta(pt);
}

// Test hooks: deterministic metadata nonces for the shared vectors. Never
// use in application code.
export const _testing = Object.freeze({ encodeMeta, sealMetaWithNonce, sealMetaRaw });
