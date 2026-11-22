// Tests the pure parts of the secret pages: the passphrase generator, the
// composer's upload flow (against a fake fetch) and the reveal page's
// helpers (preview allowlist, download names, tombstones, link parsing).
import { test } from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";

import * as e2e from "../../internal/web/static/js/e2e.js";
import WORDS from "../../internal/web/static/js/eff-wordlist.js";
import * as passgen from "../../internal/web/static/js/passgen.js";
import * as composer from "../../internal/web/static/js/secret-new.js";
import * as reveal from "../../internal/web/static/js/secret-open.js";

// ---- passgen ----

test("wordlist is the EFF large list, byte for byte", () => {
  assert.equal(WORDS.length, 7776);
  assert.equal(new Set(WORDS).size, 7776);
  // Rebuild the original file: five dice digits, a tab, the word.
  let src = "";
  WORDS.forEach((w, i) => {
    let dice = "";
    for (let n = i, k = 0; k < 5; k++, n = Math.floor(n / 6)) dice = String((n % 6) + 1) + dice;
    src += `${dice}\t${w}\n`;
  });
  assert.equal(createHash("sha256").update(src).digest("hex"), "addd35536511597a02fa0a9ff1e5284677b8883b83e986e43f15a3db996b903e");
});

test("randomInt rejects draws that would bias the result", () => {
  // n = 3: the largest multiple of 3 below 2^32 is 2^32 - 1, so 2^32 - 1
  // itself must be redrawn.
  const draws = [2 ** 32 - 1, 2 ** 32 - 1, 7];
  const rand = (buf) => {
    buf[0] = draws.shift();
  };
  assert.equal(passgen.randomInt(3, rand), 1);
  assert.equal(draws.length, 0);
  assert.throws(() => passgen.randomInt(0));
  assert.throws(() => passgen.randomInt(2 ** 32 + 1));
  assert.throws(() => passgen.randomInt(1.5));
  assert.equal(passgen.randomInt(1), 0);
});

test("randomInt is roughly uniform", () => {
  const n = 7;
  const counts = new Array(n).fill(0);
  const total = 70000;
  for (let i = 0; i < total; i++) counts[passgen.randomInt(n)]++;
  for (const c of counts) assert.ok(Math.abs(c - total / n) < total / n / 10, `count ${c}`);
});

test("passphrase and password", () => {
  const p = passgen.passphrase();
  // Four list words contain a hyphen themselves, so count by the list.
  const words = p.split("-");
  assert.ok(words.length >= 6 && words.length <= 12);
  const spaced = passgen.passphrase(6, " ").split(" ");
  assert.equal(spaced.length, 6);
  for (const w of spaced) assert.ok(WORDS.includes(w), w);
  const pw = passgen.password();
  assert.equal(pw.length, passgen.DEFAULT_LENGTH);
  for (const c of pw) assert.ok(passgen.CHARSET.includes(c));
  assert.ok(!/[0O1lIo]/.test(passgen.CHARSET));
  assert.ok(passgen.entropyBits(7776, 6) > 77);
  assert.ok(passgen.entropyBits(passgen.CHARSET.length, passgen.DEFAULT_LENGTH) > 127);
  assert.throws(() => passgen.password(10, "aa"));
  assert.notEqual(passgen.passphrase(), passgen.passphrase());
});

// ---- composer ----

test("prepare pads text and enforces limits", () => {
  const limits = { textMax: 2048, maxBytes: 10 };
  const t = composer.prepare({ kind: "text", text: "héllo" }, limits);
  assert.equal(t.blob.size, 1024);
  assert.deepEqual(t.meta, { kind: "text", name: "", type: "text/plain; charset=utf-8", size: 6 });
  assert.throws(() => composer.prepare({ kind: "text", text: "" }, limits), /Write the secret/);
  assert.throws(() => composer.prepare({ kind: "text", text: "x".repeat(2049) }, limits), /at most/);
  const f = new File([new Uint8Array(10)], "a.png", { type: "image/png" });
  assert.equal(composer.prepare({ kind: "file", file: f }, limits).meta.type, "image/png");
  assert.throws(() => composer.prepare({ kind: "file", file: new File([new Uint8Array(11)], "b") }, limits), /at most/);
  assert.throws(() => composer.prepare({ kind: "file" }, limits), /Choose a file/);
});

test("sendWithRetry retries server and network errors only", async () => {
  const sleep = async () => {};
  let n = 0;
  const flaky = async () => {
    n++;
    if (n === 1) throw new TypeError("network");
    if (n === 2) return new Response(JSON.stringify({ error: "busy" }), { status: 503 });
    return new Response("{}", { status: 200 });
  };
  assert.equal((await composer.sendWithRetry(flaky, { sleep })).status, 200);
  assert.equal(n, 3);

  n = 0;
  const refused = async () => {
    n++;
    return new Response(JSON.stringify({ error: "Chunk 0 must be 1040 bytes." }), { status: 400 });
  };
  await assert.rejects(composer.sendWithRetry(refused, { sleep }), /Chunk 0 must be 1040 bytes/);
  assert.equal(n, 1);

  n = 0;
  const down = async () => {
    n++;
    return new Response("", { status: 500 });
  };
  await assert.rejects(composer.sendWithRetry(down, { sleep, attempts: 3 }), (err) => err.status === 500);
  assert.equal(n, 3);
});

// A fake server for createSecret: it records every request and fails the
// first PUT of chunk 1 once, so the retry can be checked.
function fakeServer() {
  const calls = [];
  const s = { chunks: [], bodies: {}, finalize: null, created: null };
  const id = e2e.encode(globalThis.crypto.getRandomValues(new Uint8Array(16)));
  let failed = false;
  globalThis.fetch = async (url, init) => {
    calls.push({ url, init });
    const json = (status, v) => new Response(JSON.stringify(v), { status, headers: { "Content-Type": "application/json" } });
    if (url === "/api/v1/secrets") {
      s.created = JSON.parse(init.body);
      return json(201, { id, upload_token: "tok", chunks: e2e.numChunks(s.created.size), chunk_size: e2e.CHUNK_SIZE });
    }
    const m = /^\/api\/v1\/secrets\/([^/]+)\/(chunks\/(\d+)|finalize)$/.exec(url);
    assert.ok(m, url);
    assert.equal(m[1], id);
    assert.equal(init.headers["X-Snap-Upload-Token"], "tok");
    assert.equal(init.headers.Authorization, undefined);
    if (m[3] !== undefined) {
      const n = Number(m[3]);
      (s.bodies[n] ??= []).push(init.body);
      if (n === 1 && !failed) {
        failed = true;
        return json(503, { error: "try again" });
      }
      s.chunks[n] = init.body;
      return json(200, { next: n + 1 });
    }
    s.finalize = JSON.parse(init.body);
    return json(200, { id, expires_at: "2026-11-23T00:00:00Z", views: 1 });
  };
  return { s, calls, id };
}

async function openWithLink(link, server, passphrase) {
  const { id, key } = e2e.parseLink(link);
  const f = server.finalize;
  const salt = e2e.decode16(f.salt);
  const keys = await e2e.deriveKeys(key, id, salt, passphrase, f.kdf);
  e2e.checkCommit(keys, e2e.decode32(f.commit));
  assert.deepEqual(e2e.decode32(f.h_auth), await e2e.hash(keys.auth));
  assert.deepEqual(e2e.decode32(f.h_probe), await e2e.hash(await e2e.probeKey(key, id)));
  const meta = await e2e.openMeta(keys.meta, reveal.b64decode(f.meta_ct));
  const parts = [];
  for await (const pt of e2e.decryptChunks(keys.enc, server.chunks)) parts.push(pt);
  return { meta, parts, keys };
}

test("createSecret uploads only ciphertext and resends a failed chunk unchanged", async () => {
  const fake = fakeServer();
  const data = globalThis.crypto.getRandomValues(new Uint8Array(65536));
  const big = new Uint8Array(e2e.CHUNK_SIZE * 2 + 5);
  for (let i = 0; i < big.length; i += 65536) big.set(data.subarray(0, Math.min(65536, big.length - i)), i);
  const file = new File([big], "big.bin", { type: "application/x-thing" });
  const link = await composer.createSecret(
    { kind: "file", file, views: 2, expiresIn: 3600, passphrase: "", note: "for ops" },
    { textMax: 1 << 20, maxBytes: 1 << 30 },
    { origin: "https://snap.test" },
  );
  assert.match(link, /^https:\/\/snap\.test\/s\/[A-Za-z0-9_-]{22}#[A-Za-z0-9_-]{43}$/);
  assert.equal(fake.s.created.size, big.length);
  assert.equal(fake.s.created.views, 2);
  assert.equal(fake.s.created.note, "for ops");
  assert.equal(fake.s.finalize.kdf, null);
  // The retried chunk was the very same ciphertext.
  assert.equal(fake.s.bodies[1].length, 2);
  assert.deepEqual(fake.s.bodies[1][0], fake.s.bodies[1][1]);
  // Nothing sent contains the key or the plaintext.
  const key = link.split("#")[1];
  for (const c of fake.calls) {
    const body = typeof c.init.body === "string" ? c.init.body : Buffer.from(c.init.body).toString("latin1");
    assert.ok(!body.includes(key) && !c.url.includes(key));
    assert.ok(!Buffer.from(body, "latin1").includes(Buffer.from(data.subarray(0, 64))));
  }
  const { meta, parts } = await openWithLink(link, fake.s, null);
  assert.deepEqual(meta, { v: 1, kind: "file", name: "big.bin", type: "application/x-thing", size: big.length });
  assert.deepEqual(Buffer.concat(parts), Buffer.from(big));
});

test("createSecret with a passphrase and text", async () => {
  const fake = fakeServer();
  const link = await composer.createSecret(
    { kind: "text", text: "pässwörd: hunter2", views: 1, expiresIn: 300, passphrase: "tidy-otter" },
    { textMax: 1 << 20, maxBytes: 1 },
    { origin: "https://snap.test/" },
  );
  assert.deepEqual(fake.s.finalize.kdf, e2e.defaultKdf());
  assert.equal(fake.s.created.kind, "text");
  assert.equal(fake.s.created.size, 1024);
  await assert.rejects(openWithLink(link, fake.s, "wrong"), (err) => err.code === "commit");
  const { meta, keys } = await openWithLink(link, fake.s, "tidy-otter");
  const text = await reveal.decryptText(keys.enc, fake.s.chunks[0], meta.size);
  assert.equal(text, "pässwörd: hunter2");
  // Each attempt has its own key.
  const again = await composer.createSecret(
    { kind: "text", text: "x", views: 1, expiresIn: 300 },
    { textMax: 1 << 20, maxBytes: 1 },
    { origin: "https://snap.test" },
  );
  assert.notEqual(again.split("#")[1], link.split("#")[1]);
});

// ---- reveal page ----

test("previews only an allowlist of raster and media types", () => {
  for (const [t, tag] of [
    ["image/png", "img"],
    ["IMAGE/JPEG; q=1", "img"],
    ["image/gif", "img"],
    ["image/webp", "img"],
    ["image/avif", "img"],
    ["video/mp4", "video"],
    ["video/webm", "video"],
    ["audio/mpeg", "audio"],
    ["audio/ogg", "audio"],
  ]) {
    assert.equal(reveal.previewOf(t)?.tag, tag, t);
  }
  for (const t of [
    "image/svg+xml",
    "application/pdf",
    "text/html",
    "application/xhtml+xml",
    "text/xml",
    "image/x-icon",
    "video/quicktime",
    "audio/",
    "audio/x<script>",
    "",
    null,
    undefined,
  ]) {
    assert.equal(reveal.previewOf(t), null, String(t));
  }
  assert.equal(reveal.previewOf("image/png; x=<svg>").type, "image/png");
});

test("safeName", () => {
  for (const [input, want] of [
    ["report.pdf", "report.pdf"],
    ["../../etc/passwd", ".._.._etc_passwd".replace(/^\.+/, "")],
    ["a/b\\c:d*e?f\"g<h>i|j", "a_b_c_d_e_f_g_h_i_j"],
    ["invoice\u202Efdp.exe", "invoicefdp.exe"],
    ["\u200Bhidden\u2066.txt", "hidden.txt"],
    [".htaccess", "htaccess"],
    ["...", "secret.bin"],
    ["", "secret.bin"],
    [null, "secret.bin"],
    ["trailing.   ", "trailing"],
    ["con.txt", "_con.txt"],
    ["LPT1", "_LPT1"],
    ["tab\there", "tabhere"],
  ]) {
    assert.equal(reveal.safeName(input), want, JSON.stringify(input));
  }
  const long = "x".repeat(300) + ".tar.gz";
  const s = reveal.safeName(long);
  assert.ok([...s].length <= 200);
  assert.ok(s.endsWith(".gz"));
});

test("tombstones say when, never who", () => {
  const opened = reveal.goneMessage({ state: "burned", burn_reason: "viewed", opened_at: "2026-11-22T14:03:00Z", ip: "203.0.113.4" });
  assert.match(opened.title, /^Opened \d{4}-\d\d-\d\d \d\d:\d\d$/);
  assert.ok(!JSON.stringify(opened).includes("203.0.113.4"));
  assert.equal(reveal.goneMessage({ state: "burned", burn_reason: "failed attempts" }).title, "Destroyed after failed passphrase attempts");
  assert.equal(reveal.goneMessage({ state: "expired" }).title, "Expired");
  assert.equal(reveal.goneMessage({ state: "revoked" }).title, "Revoked by the sender");
  assert.equal(reveal.goneMessage({}).title, "Nothing here");
  assert.equal(reveal.viewsLeftText(1), "1 view left");
  assert.equal(reveal.viewsLeftText(3), "3 views left");
  assert.equal(reveal.formatWhen("nonsense"), "—");
  assert.equal(reveal.humanBytes(1536), "1.5 KB");
});

test("parseLocation and b64decode", () => {
  const id = globalThis.crypto.getRandomValues(new Uint8Array(16));
  const key = e2e.newLinkKey();
  const got = reveal.parseLocation(`/s/${e2e.encode(id)}`, e2e.encode(key));
  assert.deepEqual(got.id, id);
  assert.deepEqual(got.key, key);
  for (const [path, frag] of [
    [`/s/${e2e.encode(id)}/`, e2e.encode(key)],
    [`/s/${e2e.encode(id)}x`, e2e.encode(key)],
    [`/x/${e2e.encode(id)}`, e2e.encode(key)],
    [`/s/${e2e.encode(id)}`, ""],
    [`/s/${e2e.encode(id)}`, e2e.encode(key) + "A"],
    [`/s/${e2e.encode(id)}`, e2e.encode(key).slice(0, 42) + "B"],
  ]) {
    assert.throws(() => reveal.parseLocation(path, frag), (err) => err.code === "link", `${path}#${frag}`);
  }
  for (const n of [0, 1, 2, 3, 1052, 4096]) {
    const b = globalThis.crypto.getRandomValues(new Uint8Array(n));
    assert.deepEqual(reveal.b64decode(e2e.encode(b)), b);
  }
  assert.throws(() => reveal.b64decode("ab+c"));
  assert.throws(() => reveal.b64decode("abcde"));
});
