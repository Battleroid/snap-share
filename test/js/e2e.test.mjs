// Tests internal/web/static/js/e2e.js against the Go vectors in
// internal/e2e/testdata/vectors.json (regenerate those with
// `make go ARGS="test ./internal/e2e -run TestVectors -update"`).
import { test } from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { copyFile, mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

import * as e2e from "../../internal/web/static/js/e2e.js";

const root = new URL("../../", import.meta.url);
const vf = JSON.parse(await readFile(new URL("internal/e2e/testdata/vectors.json", root), "utf8"));
const FULL = e2e.CHUNK_SIZE + e2e.TAG_SIZE;

const hex = (s) => Uint8Array.from(s.match(/../g) ?? [], (h) => parseInt(h, 16));
const toHex = (b) => Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
const sha = (b) => createHash("sha256").update(b).digest("hex");

function concat(parts) {
  const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
  let off = 0;
  for (const p of parts) {
    out.set(p, off);
    off += p.length;
  }
  return out;
}

function gen(n) {
  const b = new Uint8Array(n);
  for (let i = 0; i < n; i++) b[i] = i % 251;
  return b;
}

function random(n) {
  const b = new Uint8Array(n);
  for (let i = 0; i < n; i += 65536) globalThis.crypto.getRandomValues(b.subarray(i, i + 65536));
  return b;
}

async function collect(it) {
  const out = [];
  for await (const x of it) out.push(x);
  return out;
}

function* piecesOf(b, size) {
  for (let i = 0; i < b.length; i += size) yield b.subarray(i, i + size);
}

function splitChunks(ct) {
  const out = [];
  let i = 0;
  for (; ct.length - i > FULL; i += FULL) out.push(ct.subarray(i, i + FULL));
  out.push(ct.subarray(i));
  return out;
}

function mutate(ct, muts) {
  let out = ct.slice();
  for (const m of muts ?? []) {
    const a = m.a ?? 0;
    const b = m.b ?? 0;
    const idx = m.index ?? 0;
    switch (m.op) {
      case "flip":
        out[m.offset ?? 0] ^= m.mask ?? 0;
        break;
      case "truncate":
        out = out.slice(0, m.length ?? 0);
        break;
      case "append":
        out = concat([out, hex(m.hex ?? "")]);
        break;
      case "swap": {
        const A = out.slice(a * FULL, (a + 1) * FULL);
        const B = out.slice(b * FULL, (b + 1) * FULL);
        out.set(B, a * FULL);
        out.set(A, b * FULL);
        break;
      }
      case "drop":
        out = concat([out.subarray(0, idx * FULL), out.subarray((idx + 1) * FULL)]);
        break;
      case "repeat":
        out = concat([out, splitChunks(out)[idx].slice()]);
        break;
      default:
        throw new Error(`unknown mutation ${m.op}`);
    }
  }
  return out;
}

const isE2E = (code) => (err) => {
  assert.ok(err instanceof e2e.E2EError, `expected E2EError, got ${err}`);
  if (code) assert.ok([].concat(code).includes(err.code), `code ${err.code}, want ${code}`);
  return true;
};

const plaintextOf = (s) => (s.plaintext_gen === "mod251" ? gen(s.plaintext_len) : hex(s.plaintext));

// JS ciphertext per stream vector, checked against Go's hashes in "streams".
const streamCT = new Map();
async function streamCiphertext(s) {
  if (!streamCT.has(s.name)) {
    streamCT.set(s.name, concat(await collect(e2e.encryptBlob(hex(s.key), new Blob([plaintextOf(s)])))));
  }
  return streamCT.get(s.name);
}

test("constants match Go", () => {
  const c = vf.constants;
  assert.equal(e2e.LABEL, c.label);
  assert.equal(e2e.ID_SIZE, c.id_size);
  assert.equal(e2e.KEY_SIZE, c.key_size);
  assert.equal(e2e.SALT_SIZE, c.salt_size);
  assert.equal(e2e.CHUNK_SIZE, c.chunk_size);
  assert.equal(e2e.TAG_SIZE, c.tag_size);
  assert.equal(e2e.NONCE_SIZE, c.nonce_size);
  assert.equal(e2e.META_SIZE, c.meta_size);
  assert.equal(e2e.SEALED_META_SIZE, c.sealed_meta_size);
  assert.equal(e2e.MIN_TEXT_PAD, c.min_text_pad);
  assert.equal(e2e.MAX_SIZE, c.max_size);
  assert.deepEqual(e2e.defaultKdf(), c.kdf_default);
  assert.deepEqual({ m: e2e.KDF_BOUNDS.minM, t: e2e.KDF_BOUNDS.minT, p: e2e.KDF_BOUNDS.p }, c.kdf_min);
  assert.deepEqual({ m: e2e.KDF_BOUNDS.maxM, t: e2e.KDF_BOUNDS.maxT, p: e2e.KDF_BOUNDS.p }, c.kdf_max);
});

test("kdf bounds", async () => {
  for (const p of vf.kdf_valid) e2e.validateKdf(p);
  const invalid = [...vf.kdf_invalid, { m: 65536.5, t: 3, p: 1 }, { m: "65536", t: 3, p: 1 }, { m: 65536, t: "3", p: 1 }, null, undefined];
  for (const p of invalid) {
    assert.throws(() => e2e.validateKdf(p), isE2E("kdf"), JSON.stringify(p));
    await assert.rejects(e2e.deriveKeys(new Uint8Array(32), new Uint8Array(16), new Uint8Array(16), "pw", p), isE2E("kdf"));
  }
});

test("links", () => {
  for (const l of vf.links) {
    assert.equal(e2e.formatLink(l.base, hex(l.id), hex(l.key)), l.link);
    assert.equal(e2e.formatLink(l.base + "/", hex(l.id), hex(l.key)), l.link);
    const p = e2e.parseLink(` ${l.link}\n`);
    assert.equal(p.base, l.base);
    assert.equal(toHex(p.id), l.id);
    assert.equal(toHex(p.key), l.key);
    assert.equal(e2e.encode(hex(l.id)).length, 22);
    assert.equal(toHex(e2e.decode16(e2e.encode(hex(l.id)))), l.id);
    assert.equal(toHex(e2e.decode32(e2e.encode(hex(l.key)))), l.key);
  }
  for (const l of vf.links_invalid) {
    assert.throws(() => e2e.parseLink(l.link), isE2E("link"), l.name);
  }
  const key = e2e.encode(hex(vf.links[0].key));
  assert.throws(
    () => e2e.parseLink(`https://h/s/short#${key}`),
    (err) => !err.message.includes(key),
  );
});

test("base64url", () => {
  for (let n = 0; n < 40; n++) {
    const b = random(n);
    assert.equal(e2e.encode(b), Buffer.from(b).toString("base64url"));
  }
  assert.throws(() => e2e.decode16("A".repeat(21) + "B"), isE2E("encoding")); // non-zero trailing bits
  assert.throws(() => e2e.decode16("A".repeat(21) + "="), isE2E("encoding"));
  assert.equal(toHex(e2e.decode16("A".repeat(21) + "Q")), "00".repeat(15) + "01");
});

test("random values", () => {
  const a = e2e.newLinkKey();
  const b = e2e.newLinkKey();
  assert.equal(a.length, 32);
  assert.notDeepEqual(a, b);
  assert.equal(e2e.newSalt().length, 16);
});

test("clear error without WebCrypto (insecure context)", async (t) => {
  assert.equal(e2e.supported(), true);
  const desc = Object.getOwnPropertyDescriptor(globalThis, "crypto");
  if (!desc?.configurable) return t.skip("globalThis.crypto is not configurable");
  Object.defineProperty(globalThis, "crypto", { value: { getRandomValues: desc.get?.call(globalThis).getRandomValues }, configurable: true });
  try {
    assert.equal(e2e.supported(), false);
    await assert.rejects(e2e.hash(new Uint8Array(1)), isE2E("unsupported"));
    await assert.rejects(e2e.probeKey(new Uint8Array(32), new Uint8Array(16)), isE2E("unsupported"));
  } finally {
    Object.defineProperty(globalThis, "crypto", desc);
  }
  assert.equal(e2e.supported(), true);
});

test("key derivation matches Go", async () => {
  for (const v of vf.keys) {
    const key = hex(v.link_key);
    const id = hex(v.id);
    const salt = hex(v.salt);
    const probe = await e2e.probeKey(key, id);
    assert.equal(toHex(probe), v.probe, `${v.name}: probe`);
    assert.equal(toHex(await e2e.hash(probe)), v.probe_hash, `${v.name}: probe hash`);
    if (v.passphrase !== null) {
      assert.equal(toHex(await e2e.passphraseKey(v.passphrase, salt, v.kdf)), v.k_pw, `${v.name}: k_pw`);
    }
    const k = await e2e.deriveKeys(key, id, salt, v.passphrase, v.kdf);
    assert.equal(toHex(k.auth), v.auth, `${v.name}: auth`);
    assert.equal(toHex(await e2e.hash(k.auth)), v.auth_hash, `${v.name}: auth hash`);
    assert.equal(toHex(k.enc), v.enc, `${v.name}: enc`);
    assert.equal(toHex(k.meta), v.meta, `${v.name}: meta`);
    assert.equal(toHex(k.commit), v.commit, `${v.name}: commit`);
    e2e.checkCommit(k, hex(v.commit));
    if (v.passphrase_nfc_utf8) {
      const nfc = new TextDecoder().decode(hex(v.passphrase_nfc_utf8));
      assert.notEqual(nfc, v.passphrase, "vector input should not already be NFC");
      const k2 = await e2e.deriveKeys(key, id, salt, nfc, v.kdf);
      assert.equal(toHex(k2.commit), v.commit, `${v.name}: NFC form`);
    }
  }
});

test("mismatched inputs fail the commitment check", async () => {
  for (const v of vf.keys_mismatch) {
    const k = await e2e.deriveKeys(hex(v.link_key), hex(v.id), hex(v.salt), v.passphrase, v.kdf);
    assert.throws(() => e2e.checkCommit(k, hex(v.commit)), isE2E("commit"), v.name);
  }
  const k = { commit: new Uint8Array(32) };
  assert.throws(() => e2e.checkCommit(k, new Uint8Array(31)), isE2E("commit"));
});

test("passphrase errors", async () => {
  const z = [new Uint8Array(32), new Uint8Array(16), new Uint8Array(16)];
  const fast = { m: 65536, t: 3, p: 1 };
  await assert.rejects(e2e.deriveKeys(...z, "", fast), isE2E("passphrase"));
  await assert.rejects(e2e.deriveKeys(...z, "a" + String.fromCharCode(0xd800), fast), isE2E("passphrase"));
  await assert.rejects(e2e.passphraseKey(42, z[2], fast), isE2E("passphrase"));
  // Without a passphrase the parameters are ignored.
  const a = await e2e.deriveKeys(...z, null, null);
  const b = await e2e.deriveKeys(...z, undefined, { m: 1, t: 1, p: 9 });
  assert.deepEqual(a, b);
});

test("single chunks match Go", async () => {
  for (const c of vf.chunks) {
    const ct = await e2e.encryptChunk(hex(c.key), c.counter, c.last, hex(c.plaintext));
    assert.equal(toHex(ct), c.ciphertext, c.name);
    const pt = await e2e.decryptChunk(hex(c.key), c.counter, c.last, hex(c.ciphertext));
    assert.equal(toHex(pt), c.plaintext, c.name);
  }
  for (const c of vf.chunks_invalid) {
    await assert.rejects(e2e.decryptChunk(hex(c.key), c.counter, c.last, hex(c.ciphertext)), isE2E("decrypt"), c.name);
  }
});

test("chunk framing rules", async () => {
  const key = await e2e.importAesKey(new Uint8Array(32));
  const C = e2e.CHUNK_SIZE;
  for (const [counter, last, n] of [
    [0, false, C - 1],
    [0, false, C + 1],
    [0, false, 0],
    [0, true, C + 1],
    [1, true, 0],
  ]) {
    await assert.rejects(e2e.encryptChunk(key, counter, last, new Uint8Array(n)), isE2E("chunk-size"));
    await assert.rejects(e2e.decryptChunk(key, counter, last, new Uint8Array(n + 16)), isE2E("decrypt"));
  }
  for (const counter of [-1, 1.5, 2 ** 53]) {
    await assert.rejects(e2e.encryptChunk(key, counter, true, new Uint8Array(1)), isE2E("too-large"));
  }
  const sizes = [
    [-1, -1, -1],
    [0, 1, 16],
    [1, 1, 17],
    [C, 1, C + 16],
    [C + 1, 2, C + 33],
    [2 * C + 1, 3, 2 * C + 49],
    [1.5, -1, -1],
    [Number.MAX_SAFE_INTEGER, Math.floor((Number.MAX_SAFE_INTEGER - 1) / C) + 1, -1],
  ];
  for (const [n, chunks, size] of sizes) {
    assert.equal(e2e.numChunks(n), chunks, `numChunks(${n})`);
    assert.equal(e2e.ciphertextSize(n), size, `ciphertextSize(${n})`);
  }
});

test("streams match Go", async () => {
  for (const s of vf.streams) {
    const pt = plaintextOf(s);
    assert.equal(sha(pt), s.plaintext_sha256, s.name);
    const chunks = await collect(e2e.encryptBlob(hex(s.key), new Blob([pt])));
    assert.equal(chunks.length, s.chunks.length, s.name);
    chunks.forEach((c, i) => {
      assert.equal(c.length, s.chunks[i].len, `${s.name} chunk ${i}`);
      assert.equal(sha(c), s.chunks[i].sha256, `${s.name} chunk ${i}`);
      assert.equal(toHex(c.subarray(c.length - 16)), s.chunks[i].tag, `${s.name} chunk ${i}`);
    });
    const ct = concat(chunks);
    assert.equal(ct.length, s.ciphertext_len);
    assert.equal(sha(ct), s.ciphertext_sha256, s.name);
    assert.equal(e2e.ciphertextSize(s.plaintext_len), s.ciphertext_len);
    assert.equal(e2e.numChunks(s.plaintext_len), s.chunks.length);
    if (s.ciphertext !== undefined) assert.equal(toHex(ct), s.ciphertext, s.name);

    // Decrypt Go's bytes when present (else ours, proven identical above),
    // fed in odd-sized pieces, as one buffer, and as a stream.
    const goCT = s.ciphertext !== undefined ? hex(s.ciphertext) : ct;
    for (const source of [piecesOf(goCT, 7777), goCT, new Blob([goCT]).stream(), [goCT.buffer]]) {
      const out = concat(await collect(e2e.decryptChunks(hex(s.key), source)));
      assert.equal(sha(out), s.plaintext_sha256, s.name);
    }
    streamCT.set(s.name, ct);
  }
});

test("tampered streams are rejected", async () => {
  const byName = new Map(vf.streams.map((s) => [s.name, s]));
  for (const v of vf.streams_invalid) {
    const s = byName.get(v.stream);
    const ct = mutate(await streamCiphertext(s), v.mutations);
    const key = hex(v.key ?? s.key);
    await assert.rejects(collect(e2e.decryptChunks(key, piecesOf(ct, 65536))), isE2E("decrypt"), v.name);
  }
});

test("text padding", () => {
  for (const p of vf.text_padding) {
    assert.equal(e2e.paddedTextLen(p.len), p.padded_len, `len ${p.len}`);
    const t = new Uint8Array(p.len).fill(7);
    const padded = e2e.padText(t);
    assert.equal(padded.length, p.padded_len);
    assert.ok(padded.subarray(p.len).every((x) => x === 0));
    assert.deepEqual(e2e.unpadText(padded, p.len), t);
  }
  assert.throws(() => e2e.unpadText(new Uint8Array(1024), 1025), isE2E("meta"));
});

test("metadata matches Go", async () => {
  for (const m of vf.meta) {
    const { plaintext, meta } = e2e._testing.encodeMeta(m.input);
    assert.equal(toHex(plaintext), m.plaintext, m.name);
    assert.deepEqual(meta, m.stored, m.name);
    const sealed = await e2e._testing.sealMetaWithNonce(hex(m.key), m.input, hex(m.nonce));
    assert.equal(toHex(sealed), m.sealed, m.name);
    assert.deepEqual(await e2e.openMeta(hex(m.key), hex(m.sealed)), m.stored, m.name);
  }
  for (const m of vf.meta_open) {
    assert.deepEqual(await e2e.openMeta(hex(m.key), hex(m.sealed)), m.meta, m.name);
  }
  for (const m of vf.meta_invalid) {
    await assert.rejects(e2e.openMeta(hex(m.key), hex(m.sealed)), isE2E(["meta", "decrypt"]), m.name);
  }
});

test("metadata encoding errors", async () => {
  const key = new Uint8Array(32);
  for (const m of [
    { kind: "file", size: 1, v: 2 },
    { size: 1 },
    { kind: "link", size: 1 },
    { kind: "file", size: -1 },
    { kind: "file", size: 1.5 },
    { kind: "file", size: 2 ** 53 },
    { kind: "file", size: 1, name: 5 },
    { kind: "file", size: 1, type: "t".repeat(1000) },
  ]) {
    await assert.rejects(e2e.sealMeta(key, m), isE2E("meta"), JSON.stringify(m));
  }
  const a = await e2e.sealMeta(key, { kind: "text", size: 3 });
  const b = await e2e.sealMeta(key, { kind: "text", size: 3 });
  assert.equal(a.length, e2e.SEALED_META_SIZE);
  assert.notDeepEqual(a.subarray(0, 12), b.subarray(0, 12));
  assert.deepEqual(await e2e.openMeta(key, a), { v: 1, kind: "text", name: "", type: "", size: 3 });
});

test("envelopes: open from the link, and re-create byte for byte", async () => {
  for (const env of vf.envelopes) {
    const { base, id, key } = e2e.parseLink(env.link);
    assert.equal(base, "https://snap.example");
    assert.equal(toHex(await e2e.hash(await e2e.probeKey(key, id))), env.probe_hash);
    const keys = await e2e.deriveKeys(key, id, hex(env.salt), env.passphrase, env.kdf);
    assert.equal(toHex(await e2e.hash(keys.auth)), env.auth_hash);
    e2e.checkCommit(keys, hex(env.commit));
    const meta = await e2e.openMeta(keys.meta, hex(env.meta_sealed));
    assert.deepEqual(meta, env.meta);
    let content = concat(await collect(e2e.decryptChunks(keys.enc, [hex(env.content)])));
    if (meta.kind === e2e.KIND_TEXT) content = e2e.unpadText(content, meta.size);
    assert.equal(toHex(content), env.plaintext, env.name);

    const body = hex(env.plaintext);
    const toEncrypt = meta.kind === e2e.KIND_TEXT ? e2e.padText(body) : body;
    const ct = concat(await collect(e2e.encryptBlob(keys.enc, new Blob([toEncrypt]))));
    assert.equal(toHex(ct), env.content);
    const nonce = hex(env.meta_sealed).subarray(0, 12);
    const sealed = await e2e._testing.sealMetaWithNonce(keys.meta, { kind: meta.kind, name: meta.name, type: meta.type, size: body.length }, nonce);
    assert.equal(toHex(sealed), env.meta_sealed);
  }
});

test("random round trips", async () => {
  const C = e2e.CHUNK_SIZE;
  const sizes = [0, 1, 15, 16, 17, 1000, C - 1, C, C + 1, 2 * C, Math.floor(Math.random() * 3 * C)];
  for (const n of sizes) {
    const kEnc = e2e.newLinkKey();
    const pt = random(n);
    const chunks = await collect(e2e.encryptBlob(kEnc, n % 2 ? new Blob([pt]) : pt));
    assert.equal(chunks.length, e2e.numChunks(n));
    const ct = concat(chunks);
    assert.equal(ct.length, e2e.ciphertextSize(n));
    const out = concat(await collect(e2e.decryptChunks(kEnc, piecesOf(ct, 1 + Math.floor(Math.random() * 200000)))));
    assert.deepEqual(out, pt, `size ${n}`);
    // Every chunk boundary truncation is detected.
    for (let cut = 0; cut < ct.length; cut += FULL) {
      await assert.rejects(collect(e2e.decryptChunks(kEnc, [ct.subarray(0, cut)])), isE2E("decrypt"));
    }
  }
  const link = e2e.newLinkKey();
  const id = e2e.newSalt();
  const salt = e2e.newSalt();
  const keys = await e2e.deriveKeys(link, id, salt, null);
  const meta = { kind: "file", name: "résumé.pdf", type: "application/pdf", size: 1234 };
  assert.deepEqual(await e2e.openMeta(keys.meta, await e2e.sealMeta(keys.meta, meta)), { v: 1, ...meta });
  const parsed = e2e.parseLink(e2e.formatLink("https://x.test/", id, link));
  assert.deepEqual([parsed.base, parsed.id, parsed.key], ["https://x.test", id, link]);
});

test("hash-wasm argon2 loads as a browser module and matches Go", async () => {
  const vendored = new URL("internal/web/static/vendor/hash-wasm/argon2.umd.min.js", root);
  // A browser evaluates the file as an ES module (no exports/module), where
  // the UMD wrapper falls back to globalThis.hashwasm. Simulate that.
  const dir = await mkdtemp(join(tmpdir(), "snap-hashwasm-"));
  try {
    const file = join(dir, "argon2.mjs");
    await copyFile(vendored, file);
    const before = globalThis.hashwasm;
    await import(pathToFileURL(file).href);
    assert.equal(typeof globalThis.hashwasm?.argon2id, "function");
    const v = vf.keys.find((k) => k.passphrase !== null && k.kdf.m === e2e.KDF_BOUNDS.minM);
    const out = await globalThis.hashwasm.argon2id({
      password: new TextEncoder().encode(v.passphrase.normalize("NFC")),
      salt: hex(v.salt),
      parallelism: v.kdf.p,
      iterations: v.kdf.t,
      memorySize: v.kdf.m,
      hashLength: 32,
      outputType: "binary",
    });
    assert.equal(toHex(out), v.k_pw);
    globalThis.hashwasm = before;
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test("CSP and portability: no eval, no Node-only APIs", async () => {
  const vendored = await readFile(new URL("internal/web/static/vendor/hash-wasm/argon2.umd.min.js", root), "utf8");
  const ours = await readFile(new URL("internal/web/static/js/e2e.js", root), "utf8");
  for (const src of [vendored, ours]) {
    assert.doesNotMatch(src, /\beval\s*\(|new\s+Function\b|\bFunction\s*\(|setTimeout\s*\(\s*["'`]/);
  }
  assert.doesNotMatch(ours, /\brequire\s*\(|\bprocess\.|\bBuffer\b|["']node:/);
});
