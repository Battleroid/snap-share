// Reveal page for one-time secrets (/s/{id}#{K_link}), and the paste box at
// /s, which opens a pasted link in place so the key never enters the
// browser history.
//
// fragment.js (a classic script in the page head) takes the key out of the
// fragment and the address bar before the page is parsed; this module picks
// it up from there and keeps it in memory only. The page asks the server for
// the secret's public parameters with the probe key, and claims a view only
// when the recipient clicks Reveal. Nothing is decrypted before the key
// commitment checks out, text is shown with textContent, and files are
// handed over as application/octet-stream downloads. Images, video and
// audio of a few allowlisted types can be previewed after a click; nothing
// is decoded automatically.

import * as e2e from "./e2e.js";
import pbInit from "../vendor/patchbay/patchbay.mjs";

export const TICKET_HEADER = "X-Snap-Ticket";

// ---- pure helpers (tested in test/js/secrets.test.mjs) ----

const PREVIEW = new Map([
  ["image/png", "img"],
  ["image/jpeg", "img"],
  ["image/gif", "img"],
  ["image/webp", "img"],
  ["image/avif", "img"],
  ["video/mp4", "video"],
  ["video/webm", "video"],
]);

// previewOf returns { tag, type } for a creator-declared MIME type that may
// be shown inline, or null. Never SVG, PDF, HTML or anything else.
export function previewOf(declared) {
  const t = String(declared ?? "").split(";")[0].trim().toLowerCase();
  if (PREVIEW.has(t)) return { tag: PREVIEW.get(t), type: t };
  if (/^audio\/[a-z0-9][a-z0-9.+-]{0,63}$/.test(t)) return { tag: "audio", type: t };
  return null;
}

// previewEl builds the opt-in preview for previewOf's { tag } from a blob
// URL: an image shown inline (no link: nothing may navigate to the blob,
// docs/security.md), video or audio in the kit MediaPlayer (no native
// controls; the caller binds it with pbInit). name is the safe file name.
export function previewEl(pv, src, name, doc = globalThis.document) {
  const el = (tag, cls) => {
    const n = doc.createElement(tag);
    if (cls) n.className = cls;
    return n;
  };
  if (pv.tag === "img") {
    const frame = el("div", "pb-media__frame pb-media__frame--contain");
    const img = el("img");
    img.src = src;
    img.alt = name;
    frame.append(img);
    return frame;
  }
  const fig = el("figure", pv.tag === "audio" ? "pb-player pb-player--audio" : "pb-player");
  fig.dataset.player = "";
  fig.setAttribute("aria-label", name);
  const media = el(pv.tag);
  media.src = src;
  media.preload = "metadata";
  if (pv.tag === "video") media.playsInline = true;
  fig.append(media);
  if (pv.tag === "audio") return fig;
  const frame = el("div", "pb-media__frame pb-media__frame--player");
  frame.append(fig);
  return frame;
}

// Removed from file names: control (Cc) and format characters (Cf: the
// invisible and bidi controls that can disguise an extension), line and
// paragraph separators, lone surrogates and U+FFFD. sp open removes the same
// set (Go's unicode.Cc, Cf, Zl, Zp); both follow
// internal/e2e/testdata/safename.json.
const UNSAFE_NAME = /[\p{Cc}\p{Cf}\p{Zl}\p{Zp}\p{Cs}\ufffd]/gu;
// Windows device names, with any extension: CON, PRN, AUX, NUL, COM0-9,
// LPT0-9, the superscript COM\u00b9-\u00b3 and LPT\u00b9-\u00b3, CONIN$ and CONOUT$.
const DEVICE_NAME = /^(con|prn|aux|nul|conin\$|conout\$|com[0-9\u00b9\u00b2\u00b3]|lpt[0-9\u00b9\u00b2\u00b3])$/u;
const MAX_NAME_BYTES = 200;
const MAX_EXT_BYTES = 16;

const utf8Len = (s) => new TextEncoder().encode(s).length;
const trimName = (s) => s.replace(/^[\p{Zs}.]+/u, "").replace(/[\p{Zs}.]+$/u, "");
const asciiLower = (s) => s.replace(/[A-Z]/g, (c) => c.toLowerCase());

// safeName makes the creator's file name safe to offer as a download name:
// the last path component, no control, invisible or bidi characters, no
// characters Windows refuses, no leading or trailing dots and spaces, at
// most 200 UTF-8 bytes (keeping a short extension), never empty or a
// Windows device name.
export function safeName(name, fallback = "secret.bin") {
  let s = String(name ?? "").normalize("NFC");
  s = s.slice(Math.max(s.lastIndexOf("/"), s.lastIndexOf("\\")) + 1);
  s = trimName(s.replace(UNSAFE_NAME, "").replace(/[:*?"<>|]/g, "_"));
  if (utf8Len(s) > MAX_NAME_BYTES) {
    const dot = s.lastIndexOf(".");
    let ext = dot > 0 ? s.slice(dot) : "";
    if (utf8Len(ext) > MAX_EXT_BYTES) ext = "";
    let n = utf8Len(ext);
    let stem = "";
    for (const c of ext ? s.slice(0, dot) : s) {
      n += utf8Len(c);
      if (n > MAX_NAME_BYTES) break;
      stem += c;
    }
    s = trimName(stem + ext);
  }
  if (!s) return fallback;
  const base = s.split(".")[0].replace(/\p{Zs}+$/u, "");
  if (DEVICE_NAME.test(asciiLower(base))) s = "_" + s;
  return s;
}

// takeFragment returns the URL fragment (without "#") that fragment.js
// stashed and removed from the address bar, and forgets it. If fragment.js
// did not run, it reads and removes the fragment itself.
export function takeFragment(g = globalThis) {
  let f = g.snapFragment;
  delete g.snapFragment;
  if (typeof f !== "string") {
    f = g.location.hash.slice(1);
    if (g.location.hash) g.history.replaceState(null, "", g.location.pathname + g.location.search);
  }
  return f;
}

// pastedLink parses a link pasted into the box at /s. It must be a whole
// secret link for this server (origin); anything else is refused before
// anything is sent. It returns { id, idText, key }, or throws E2EError
// "link", with err.otherHost set when the link names another server.
export function pastedLink(text, origin) {
  const { base, id, key } = e2e.parseLink(String(text ?? ""));
  let other = "";
  try {
    const u = new URL(base);
    if (u.origin !== origin || u.pathname !== "/") other = u.host || base;
  } catch {
    other = base;
  }
  if (other) {
    const err = new e2e.E2EError("link", "the link is for another server");
    err.otherHost = other;
    throw err;
  }
  return { id, idText: e2e.encode(id), key };
}

const pad2 = (n) => String(n).padStart(2, "0");

// formatWhen renders an RFC 3339 time as local "2026-11-22 14:03".
export function formatWhen(iso) {
  const d = new Date(iso);
  if (!iso || Number.isNaN(d.getTime())) return "—";
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())} ${pad2(d.getHours())}:${pad2(d.getMinutes())}`;
}

// whenNode is a time for a meta line: the kit's relative time ("in 6 days"),
// with the absolute local time as its text until the kit binds it and in its
// title.
export function whenNode(iso) {
  const abs = formatWhen(iso);
  if (abs === "—") return document.createTextNode(abs);
  const t = document.createElement("time");
  t.className = "pb-reltime";
  t.dateTime = new Date(iso).toISOString();
  t.title = abs;
  t.textContent = abs;
  return t;
}

export function viewsLeftText(n) {
  return n === 1 ? "1 view left" : `${n} views left`;
}

export function humanBytes(n) {
  if (!Number.isFinite(n) || n < 0) return "—";
  if (n < 1000) return `${n} B`;
  const units = "KMGTPE";
  let v = n;
  let i = -1;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  return `${v >= 100 ? v.toFixed(0) : v.toFixed(1)} ${units[i]}B`;
}

// goneMessage turns a tombstone (410 body) into what the page says. It
// never shows who opened the secret: the server doesn't send that.
export function goneMessage(body) {
  const state = body?.state;
  if (state === "expired") return { title: "Expired", body: "Ask the sender for a new link." };
  if (state === "revoked" && body.burn_reason === "removed by an admin") return { title: "Removed by the site's admin", body: "" };
  if (state === "revoked") return { title: "Revoked by the sender", body: "" };
  if (state === "burned" && body.burn_reason === "failed attempts") {
    return { title: "Destroyed after failed passphrase attempts", body: "Ask the sender for a new link." };
  }
  if (state === "burned") {
    const when = body.opened_at ? `Opened ${formatWhen(body.opened_at)}` : "Already opened";
    return { title: when, body: "It can't be opened again. If that wasn't you, someone else has seen it: tell the sender." };
  }
  return NOTHING;
}

export const NOTHING = Object.freeze({ title: "Nothing here", body: "The link may be wrong or incomplete, or the secret was deleted." });

// b64decode decodes unpadded base64url of any length.
export function b64decode(s) {
  if (typeof s !== "string" || !/^[A-Za-z0-9_-]*$/.test(s) || s.length % 4 === 1) {
    throw new e2e.E2EError("encoding", "invalid base64url value");
  }
  const std = s.replace(/-/g, "+").replace(/_/g, "/") + "===".slice((s.length + 3) % 4);
  const bin = atob(std);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

// parseLocation reads the id from the path and K_link from the fragment.
// It throws E2EError "link" for anything that isn't exactly /s/{22}#{43}.
export function parseLocation(pathname, fragment) {
  const m = /^\/s\/([A-Za-z0-9_-]{22})$/.exec(pathname);
  if (!m) throw new e2e.E2EError("link", "invalid secret link: bad id");
  const id = e2e.decode16(m[1]);
  if (!fragment) throw new e2e.E2EError("link", "invalid secret link: no key fragment");
  let key;
  try {
    key = e2e.decode32(fragment);
  } catch {
    throw new e2e.E2EError("link", "invalid secret link: bad key");
  }
  return { id, idText: m[1], key };
}

function concat(parts) {
  const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
  let off = 0;
  for (const p of parts) {
    out.set(p, off);
    off += p.length;
  }
  return out;
}

// decryptText decrypts a text secret's inline ciphertext and drops the padding.
export async function decryptText(kEnc, ct, size) {
  const parts = [];
  for await (const pt of e2e.decryptChunks(kEnc, ct)) parts.push(pt);
  const full = concat(parts);
  const text = new TextDecoder("utf-8").decode(e2e.unpadText(full, size));
  full.fill(0);
  return text;
}

// ---- page ----

async function post(url, body, headers = {}) {
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json", ...headers },
    body: JSON.stringify(body),
    credentials: "same-origin",
    cache: "no-store",
    referrerPolicy: "no-referrer",
  });
  let data = {};
  try {
    data = await res.json();
  } catch {
    // empty or not JSON
  }
  return { status: res.status, data };
}

function page(root, path, fragment) {
  const ui = pageUI(root);
  if (!e2e.supported()) return ui.unsupported();
  if (path === "/s") return pasteBox(root, ui);
  let link;
  try {
    link = parseLocation(path, fragment);
  } catch {
    return ui.problem(
      fragment ? NOTHING : { title: "The link is incomplete", body: "Copy the whole link, including the part after #, and open it again." },
      "warn",
    );
  }
  reveal(root, ui, link);
}

// pasteBox runs the form at /s: the pasted link is opened on this page,
// without navigating, so it never becomes a history entry.
function pasteBox(root, ui) {
  const $ = (sel) => root.querySelector(sel);
  const form = $("[data-paste-form]");
  const input = form.querySelector('input[name="link"]');
  const label = $("[data-paste-label]");
  const help = $("[data-paste-help]");
  const fieldError = (msg) => {
    label.toggleAttribute("data-invalid", Boolean(msg));
    $("[data-paste-help-text]").textContent = msg;
    help.hidden = !msg;
  };
  form.addEventListener("submit", (e) => {
    e.preventDefault();
    let link;
    try {
      link = pastedLink(input.value, location.origin);
    } catch (err) {
      return fieldError(
        err?.otherHost
          ? `This link is for ${err.otherHost}. Open it there.`
          : "That is not a whole secret link. Copy all of it, including the part after #.",
      );
    }
    input.value = "";
    fieldError("");
    reveal(root, ui, link);
  });
  ui.show("paste");
  $("[data-paste-btn]").disabled = false; // the handler is in place
  input.focus();
}

function pageUI(root) {
  const $ = (sel) => root.querySelector(sel);
  const views = [...root.querySelectorAll("[data-view]")];
  const show = (name) => {
    for (const v of views) v.hidden = v.dataset.view !== name;
  };
  // A page that ends here (kit Result): warn for gone, expired and bad
  // links, down for failures.
  const problem = (msg, status = "") => {
    const box = $("[data-problem]");
    box.dataset.status = status === "down" ? "down" : "warn";
    box.setAttribute("role", status === "down" ? "alert" : "status");
    $("[data-problem-icon]").className = `pb-icon pb-icon--status-${box.dataset.status} app-auth-result__glyph`;
    $("[data-problem-title]").textContent = msg.title;
    const b = $("[data-problem-body]");
    b.textContent = msg.body || "";
    b.hidden = !msg.body;
    $("[data-after]").hidden = true;
    show("problem");
  };
  const tampered = () =>
    problem({ title: "This secret failed verification", body: "It was not opened. The link or the server's copy has been changed." }, "down");
  const unsupported = () =>
    problem({ title: "This browser can't open secrets here", body: "Secrets are decrypted in the browser, which needs a secure (https) connection." }, "down");
  return { show, problem, tampered, unsupported };
}

// reveal runs the reveal flow for a parsed link { id, idText, key }.
function reveal(root, ui, link) {
  const $ = (sel) => root.querySelector(sel);
  const { show, problem, tampered } = ui;
  const busyError = { title: "Too many requests", body: "Wait a minute and reload the page." };
  show("loading");
  const base = `/api/v1/secrets/${link.idText}`;

  (async () => {
    const probe = e2e.encode(await e2e.probeKey(link.key, link.id));
    const { status, data } = await post(`${base}/info`, { probe });
    if (status === 410) return problem(goneMessage(data));
    if (status === 429) return problem(busyError, "warn");
    if (status !== 200) return problem(NOTHING);
    ready(probe, data);
  })().catch(() => problem({ title: "The secret could not be loaded", body: "Check the connection and reload the page." }, "down"));

  function ready(probe, info) {
    let salt;
    try {
      if (info.kind !== e2e.KIND_TEXT && info.kind !== e2e.KIND_FILE) throw new Error("kind");
      salt = e2e.decode16(info.salt);
      // A malicious server could weaken or inflate the passphrase cost.
      if (info.kdf !== null) e2e.validateKdf(info.kdf);
    } catch {
      return problem({ title: "This secret's settings are unsafe", body: "It was not opened." }, "down");
    }
    const hasPass = info.kdf !== null;
    $("[data-band-text]").textContent = viewsLeftText(info.views_left);
    $("[data-meta-kind]").textContent = info.mode === "sealed" ? `${info.kind} · server-encrypted` : info.kind;
    $("[data-meta-size]").textContent = info.kind === e2e.KIND_TEXT ? `≤ ${humanBytes(info.size)}` : humanBytes(info.size);
    const expires = $("[data-meta-expires]");
    expires.replaceChildren(whenNode(info.expires_at));
    pbInit(expires, { only: ["reltime"] });
    $("[data-reveal-hint]").textContent =
      info.views_left === 1 ? "Revealing uses the last view; then the secret is destroyed." : "Revealing uses one view.";
    const field = $("[data-pass-field]");
    const passInput = field.querySelector("input");
    const label = $("[data-pass-label]");
    const help = $("[data-pass-help]");
    field.hidden = !hasPass;
    show("ready");
    if (hasPass) passInput.focus();

    const btn = $("[data-reveal-btn]");
    const setBusy = (on, label = "Reveal") => {
      btn.disabled = on;
      btn.setAttribute("aria-busy", String(on));
      $("[data-reveal-icon]").className = on ? "pb-icon pb-icon--refresh pb-icon--spin" : "pb-icon pb-icon--unlock";
      $("[data-reveal-label]").textContent = label;
    };
    const fieldError = (msg) => {
      label.toggleAttribute("data-invalid", Boolean(msg));
      $("[data-pass-help-text]").textContent = msg;
      help.hidden = !msg;
    };

    $("[data-reveal-form]").addEventListener("submit", async (e) => {
      e.preventDefault();
      if (btn.disabled) return;
      const pass = hasPass ? passInput.value : null;
      if (hasPass && !pass) return fieldError("Enter the passphrase.");
      fieldError("");
      setBusy(true, hasPass ? "Deriving key…" : "Opening…");
      try {
        const keys = await e2e.deriveKeys(link.key, link.id, salt, pass, info.kdf);
        setBusy(true, "Opening…");
        const { status, data } = await post(`${base}/claim`, { probe, auth: e2e.encode(keys.auth) });
        if (status === 403) {
          const left = Number(data.attempts_left);
          if (data.state === "burned" || left <= 0) {
            return problem(goneMessage({ state: "burned", burn_reason: "failed attempts" }));
          }
          setBusy(false);
          passInput.select();
          return fieldError(`Wrong passphrase. ${left} ${left === 1 ? "attempt" : "attempts"} left.`);
        }
        if (status === 410) return problem(goneMessage(data));
        if (status === 429) {
          setBusy(false);
          return fieldError("Too many attempts. Wait a minute and try again.");
        }
        if (status !== 200) return problem(NOTHING);
        passInput.value = "";
        await claimed(data, keys, info);
      } catch (err) {
        if (err instanceof e2e.E2EError && ["commit", "decrypt", "meta", "encoding"].includes(err.code)) return tampered();
        problem({ title: "The secret could not be opened", body: "Check the connection and reload the page." }, "down");
      }
    });
    btn.disabled = false; // the handler is in place
  }

  async function claimed(data, keys, info) {
    e2e.checkCommit(keys, e2e.decode32(data.commit)); // before decrypting anything
    const meta = await e2e.openMeta(keys.meta, b64decode(data.meta_ct));
    if (meta.kind !== info.kind || meta.kind !== data.kind) throw new e2e.E2EError("meta", "kind mismatch");
    const after = $("[data-after]");
    after.textContent =
      data.views_left === 0
        ? "That was the last view: the secret is now destroyed on the server."
        : `${viewsLeftText(data.views_left)} for others.`;

    if (meta.kind === e2e.KIND_TEXT) {
      const text = await decryptText(keys.enc, b64decode(data.content), meta.size);
      $("[data-text-body]").textContent = text;
      $("[data-text-gutter]").textContent = text.split("\n").map((_, i) => i + 1).join("\n");
      show("text");
      after.hidden = false;
      return;
    }
    $("[data-file-name]").textContent = safeName(meta.name);
    $("[data-file-type]").textContent = `${meta.type || "unknown type"} · ${humanBytes(meta.size)}`;
    show("file");
    after.hidden = false;
    await download(data, keys, meta);
  }

  async function download(data, keys, meta) {
    const bar = $("[data-file-bar]");
    const pct = $("[data-file-pct]");
    const errBox = $("[data-file-error]");
    const retry = $("[data-file-retry]");
    const setPct = (p) => {
      bar.value = p;
      bar.textContent = `${p}%`;
      pct.textContent = `${p}%`;
    };
    const run = async () => {
      errBox.hidden = true;
      setPct(0);
      const res = await fetch(`${base}/blob`, {
        headers: { [TICKET_HEADER]: data.ticket },
        credentials: "same-origin",
        cache: "no-store",
        referrerPolicy: "no-referrer",
      });
      if (!res.ok || !res.body) throw new Error(`download ${res.status}`);
      let got = 0;
      const counted = res.body.pipeThrough(
        new TransformStream({
          transform(chunk, ctl) {
            got += chunk.byteLength;
            setPct(Math.min(99, Math.floor((got / data.ct_size) * 100)));
            ctl.enqueue(chunk);
          },
        }),
      );
      const parts = [];
      let size = 0;
      for await (const pt of e2e.decryptChunks(keys.enc, counted)) {
        parts.push(pt);
        size += pt.length;
      }
      // Only now, with the final chunk verified, is the content trusted.
      if (size !== meta.size) throw new e2e.E2EError("meta", "size mismatch");
      setPct(100);
      const a = $("[data-download]");
      a.href = URL.createObjectURL(new Blob(parts, { type: "application/octet-stream" }));
      a.download = safeName(meta.name);
      $("[data-download-wrap]").hidden = false;
      // Previews are opt-in: decoding a file the sender chose exposes the
      // browser's image and media decoders, so nothing is decoded until the
      // recipient asks.
      const pv = previewOf(meta.type);
      if (pv) {
        const wrap = $("[data-preview-wrap]");
        wrap.hidden = false;
        $("[data-preview-btn]").addEventListener(
          "click",
          () => {
            const view = previewEl(pv, URL.createObjectURL(new Blob(parts, { type: pv.type })), safeName(meta.name));
            $("[data-preview-frame]").replaceChildren(view);
            $("[data-preview]").hidden = false;
            wrap.hidden = true;
            // The kit's MediaPlayer for video and audio, bound as it is
            // inserted; an image needs nothing.
            if (pv.tag !== "img") pbInit(view, { only: ["player"] });
          },
          { once: true },
        );
      }
      $("[data-file-status]").hidden = true;
      // The final chunk verified: the server may delete the file if no
      // views remain.
      post(`${base}/done`, {}, { [TICKET_HEADER]: data.ticket }).catch(() => {});
    };
    const attempt = async () => {
      try {
        await run();
      } catch (err) {
        if (err instanceof e2e.E2EError && err.code !== "encoding") return tampered();
        $("[data-file-error-title]").textContent = "The download failed.";
        errBox.hidden = false;
      }
    };
    retry.addEventListener("click", attempt);
    await attempt();
  }
}

if (globalThis.document && globalThis.location) {
  // fragment.js has already taken the key out of the address bar (and so
  // out of the session history); from here on it lives only in this closure.
  const fragment = takeFragment();
  const root = document.querySelector("[data-secret-open]");
  if (root) page(root, location.pathname, fragment);
}
