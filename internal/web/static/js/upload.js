// DropZone and resumable uploads for the composer.
//
// While the Upload tab is open the whole page is the drop target (an
// enter/leave counter keeps the "over" state steady while the pointer crosses
// child elements), and an image pasted from the clipboard is uploaded too.
// Each file is declared (POST /api/v1/uploads), sent in parts (PUT ?offset=,
// resuming from the server's offset after a 409 or a dropped connection) and
// completed. The kit UploadQueue shows a row per file and reports its clicks:
// Cancel aborts the transfer (and frees the declared upload), Retry sends the
// file again from the server's offset, Remove forgets the row; Cancel all, the
// aggregate line and the warning before leaving mid-transfer are the kit's.
// A finished file leaves the queue as a result row with Copy link, Open and
// its QR code. When two or more files have been uploaded since the album
// prompt was last answered, one prompt offers to share them as one album.

import { pbBusy, pbNoticeInto, pbAnnounce } from "../vendor/patchbay/patchbay.mjs";
import { init as bind } from "./kit.js";
import { el, icon, humanBytes, resultRow, addRecent, shortLink, arm } from "./item-rows.js";

export { humanBytes, addRecent };

function readOptions() {
  const form = document.getElementById("composer-options");
  const out = {};
  if (!form) return out;
  for (const [k, v] of new FormData(form)) if (v !== "") out[k] = String(v);
  return out;
}

class HTTPError extends Error {
  constructor(status, body) {
    super((body && body.error) || `Request failed (${status}).`);
    this.status = status;
    this.body = body;
  }
}

// canceled is what a transfer throws when the person canceled it.
const canceled = () => Object.assign(new Error("Canceled."), { canceled: true });

async function api(method, url, body, signal) {
  const res = await fetch(url, {
    method,
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: "same-origin",
    signal,
  }).catch((err) => { throw err?.name === "AbortError" ? canceled() : err; });
  let data = null;
  try { data = await res.json(); } catch { /* empty body */ }
  if (!res.ok) throw new HTTPError(res.status, data);
  return data;
}

function putPart(id, offset, blob, onProgress, signal) {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) return reject(canceled());
    const x = new XMLHttpRequest();
    x.open("PUT", `/api/v1/uploads/${encodeURIComponent(id)}?offset=${offset}`);
    x.responseType = "json";
    x.setRequestHeader("Accept", "application/json");
    x.upload.addEventListener("progress", (e) => onProgress(e.loaded));
    x.addEventListener("load", () => (x.status >= 200 && x.status < 300 ? resolve(x.response) : reject(new HTTPError(x.status, x.response))));
    x.addEventListener("error", () => reject(Object.assign(new Error("Connection lost."), { retry: true })));
    x.addEventListener("timeout", () => reject(Object.assign(new Error("Timed out."), { retry: true })));
    x.addEventListener("abort", () => reject(canceled()));
    signal?.addEventListener("abort", () => x.abort(), { once: true });
    x.send(blob);
  });
}

async function serverOffset(id, signal) {
  const res = await fetch(`/api/v1/uploads/${encodeURIComponent(id)}`, { method: "HEAD", credentials: "same-origin", signal });
  if (!res.ok) throw new HTTPError(res.status, null);
  return Number(res.headers.get("Upload-Offset")) || 0;
}

function sleep(ms, signal) {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) return reject(canceled());
    const t = setTimeout(resolve, ms);
    signal?.addEventListener("abort", () => { clearTimeout(t); reject(canceled()); }, { once: true });
  });
}

// uploadFile runs the declare / parts / complete sequence for one file.
// state remembers the declared upload ({ id, part }) so that a retry carries
// on from the server's offset instead of starting over; if the server no
// longer knows it, the file is declared again. onProgress(sent, total) runs
// as bytes go; onProcessing() once they are all in and the server finishes
// the file. signal cancels it (the error has canceled set).
export async function uploadFile(file, { options = readOptions(), signal, onProgress = () => {}, onProcessing = () => {}, state = {} } = {}) {
  let offset = 0;
  if (state.id) {
    try { offset = await serverOffset(state.id, signal); } catch (err) {
      if (err.canceled || signal?.aborted) throw canceled();
      state.id = "";
    }
  }
  if (!state.id) {
    const init = await api("POST", "/api/v1/uploads", { name: file.name, size: file.size, type: file.type, options }, signal);
    state.id = init.id;
    state.part = init.part_size || 8 << 20;
    offset = 0;
  }
  const part = state.part || 8 << 20;
  let failures = 0;
  while (offset < file.size) {
    const end = Math.min(offset + part, file.size);
    try {
      const start = offset;
      const res = await putPart(state.id, offset, file.slice(offset, end), (loaded) => onProgress(start + loaded, file.size), signal);
      offset = res.offset;
      failures = 0;
    } catch (err) {
      if (err.canceled) throw err;
      if (err.status === 409 && err.body && typeof err.body.offset === "number") {
        offset = err.body.offset;
        continue;
      }
      if ((err.retry || err.status >= 500) && failures < 6) {
        failures++;
        await sleep(Math.min(500 * 2 ** failures, 15000), signal);
        try { offset = await serverOffset(state.id, signal); } catch (e) { if (e.canceled) throw e; /* retry the same offset */ }
        continue;
      }
      throw err;
    }
  }
  onProgress(file.size, file.size);
  onProcessing();
  const item = await api("POST", `/api/v1/uploads/${encodeURIComponent(state.id)}/complete`, undefined, signal);
  state.id = "";
  return item;
}

// release frees an upload the server still holds (best effort).
function release(state) {
  if (!state.id) return;
  const id = state.id;
  state.id = "";
  fetch(`/api/v1/uploads/${encodeURIComponent(id)}`, { method: "DELETE", credentials: "same-origin" }).catch(() => {});
}

// albumSet is the files uploaded since the album prompt was last answered:
// add(item, order) records one (order is its place among the picked files, so
// the album keeps the order they were picked in), items() lists them in that
// order, and add returns true once there are two or more.
export function albumSet() {
  let list = [];
  return {
    add(item, order = list.length) {
      if (!item || list.some((e) => e.item.id === item.id)) return list.length >= 2;
      list.push({ item, order });
      list.sort((a, b) => a.order - b.order);
      return list.length >= 2;
    },
    items: () => list.map((e) => e.item),
    remove(items) {
      const ids = new Set(items.map((i) => i.id));
      list = list.filter((e) => !ids.has(e.item.id));
    },
    clear() { list = []; },
    get count() { return list.length; },
  };
}

// albumBody is the POST /api/v1/albums body for the files: the composer's
// options, the title when one is given, and the items' ids in order.
export function albumBody(title, items, options = {}) {
  const body = { ...options };
  const t = String(title || "").trim();
  if (t) body.title = t;
  body.items = items.map((i) => i.id);
  return body;
}

// offerTitle is the album prompt's heading.
export const offerTitle = (n) => `${n} files uploaded`;

// linkMeta is the "link + copy" content shown when something is created.
export function linkMeta(url) {
  const frag = document.createDocumentFragment();
  const a = el("a", "pb-link pb-mono", shortLink(url));
  a.href = url;
  const b = el("button", "pb-btn pb-btn--ghost pb-btn--icon pb-btn--sm");
  b.type = "button";
  b.dataset.copy = "";
  b.dataset.copyValue = url;
  b.setAttribute("aria-label", "Copy link");
  b.append(el("span", "pb-icon pb-icon--copy"));
  frag.append(a, " ", b);
  return frag;
}

// albumPrompt is the one "N files uploaded" prompt in box: offering the
// album, then (once made) its link. Another upload while it is open only
// changes its count; dismissing it forgets the files.
function albumPrompt(box, set) {
  const wrap = el("div", "app-stack");
  wrap.dataset.offer = "open";
  const prompt = pbNoticeInto(wrap, { title: offerTitle(set.count), body: "Share them together as one album, with one link.", dismissible: true });
  const result = el("div");
  wrap.append(result);

  const form = el("form", "pb-notice__actions app-album-offer");
  form.method = "dialog";
  const input = el("input");
  Object.assign(input, { name: "title", type: "text", maxLength: 200, placeholder: "Album title (optional)", autocomplete: "off" });
  input.setAttribute("aria-label", "Album title");
  const field = el("span", "pb-input app-album-offer__title");
  field.append(input);
  const create = el("button", "pb-btn pb-btn--sm pb-btn--primary");
  create.type = "submit";
  create.dataset.busyLabel = "Creating…";
  create.append(icon("folder"), "Create album");
  form.append(field, create);
  prompt.insertBefore(form, prompt.querySelector("[data-x]"));

  prompt.addEventListener("click", (e) => {
    if (!e.target.closest("[data-x]")) return;
    set.clear();
    wrap.dataset.offer = "dismissed";
    result.remove();
  });
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const items = set.items();
    pbBusy(true, create);
    try {
      const album = await api("POST", "/api/v1/albums", albumBody(input.value, items, readOptions()));
      set.remove(items);
      wrap.dataset.offer = "done";
      const ok = pbNoticeInto(wrap, { variant: "ok", title: "Album created", body: linkMeta(album.url) });
      arm(ok);
      addRecent(album);
    } catch (err) {
      pbNoticeInto(result, { variant: "down", title: err.message });
    } finally {
      pbBusy(false, create);
    }
  });
  box.replaceChildren(wrap);
  return wrap;
}

// showOffer puts the prompt in box, or updates the one that is there.
function showOffer(box, set) {
  if (set.count < 2) return;
  const cur = box.firstElementChild;
  if (cur?.dataset.offer === "open") {
    const t = cur.querySelector(".pb-notice__title");
    if (t) t.textContent = offerTitle(set.count);
    return;
  }
  albumPrompt(box, set);
}

function init(zone) {
  const input = zone.querySelector("input[type=file]");
  const icn = zone.querySelector(".pb-drop__icon");
  const title = zone.querySelector(".pb-drop__title");
  const hint = zone.querySelector(".pb-drop__hint");
  const files = document.querySelector("[data-filelist]");
  const queueBox = files.querySelector("[data-upq]");
  const done = files.querySelector("[data-uploaded]");
  const offers = document.querySelector("[data-album-offers]");
  const idleTitle = title.textContent;
  const idleHint = [...hint.childNodes].map((n) => n.cloneNode(true));
  const max = () => Number(zone.dataset.max) || Infinity;
  const blocked = new Set((zone.dataset.blocked || "").split(",").filter(Boolean));
  const offer = albumSet();
  let depth = 0;
  // The kit binds the queue on load; do it now in case this ran first.
  bind(queueBox);

  const setIcon = (name) => { icn.className = `pb-icon pb-icon--${name} pb-drop__icon`; };
  const idle = () => {
    delete zone.dataset.state;
    setIcon("upload");
    title.textContent = idleTitle;
    hint.replaceChildren(...idleHint.map((n) => n.cloneNode(true)));
  };
  const over = (n) => {
    zone.dataset.state = "over";
    setIcon("download");
    title.textContent = n > 0 ? `Release to upload ${n} ${n === 1 ? "file" : "files"}` : "Release to upload";
  };

  // The page takes drops and pastes only while the Upload tab is open. The
  // composer's Secret tab has its own drop zone; the page-wide target must
  // never catch those files (they would be uploaded unencrypted).
  const panel = document.getElementById("panel-upload");
  const uploadOpen = () => !!panel && !panel.hidden;
  const hasFiles = (e) => uploadOpen() && [...(e.dataTransfer?.types || [])].includes("Files");
  const count = (e) => [...(e.dataTransfer?.items || [])].filter((i) => i.kind === "file").length;

  window.addEventListener("dragenter", (e) => {
    if (!hasFiles(e)) return;
    e.preventDefault();
    if (depth++ === 0) over(count(e));
  });
  window.addEventListener("dragover", (e) => {
    if (!hasFiles(e)) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = "copy";
  });
  window.addEventListener("dragleave", (e) => {
    if (!hasFiles(e)) return;
    if (--depth <= 0) { depth = 0; idle(); }
  });
  window.addEventListener("drop", (e) => {
    if (!hasFiles(e)) return;
    e.preventDefault();
    depth = 0;
    idle();
    accept([...e.dataTransfer.files]);
  });
  input.addEventListener("change", () => {
    accept([...input.files]);
    input.value = "";
  });

  // Pasting an image (a screenshot, a copied picture) uploads it. Only files
  // on the clipboard count, never while typing in a field, and never while
  // another tab is open (the Secret tab must not upload unencrypted).
  document.addEventListener("paste", (e) => {
    if (!uploadOpen()) return;
    const t = e.target;
    if (t instanceof Element && t.closest("input, textarea, select, [contenteditable]")) return;
    const pasted = [...(e.clipboardData?.files || [])];
    if (!pasted.length) return;
    e.preventDefault();
    accept(pasted.map((f, i) => (f.name && f.name !== "image.png" ? f : new File([f], pastedName(f, i), { type: f.type }))));
  });

  // jobs maps a queue row to its file's job: { file, it, order, upload, ctl, reason }.
  const jobs = new Map();
  const queue = [];
  let running = 0;
  let picks = 0;

  function pastedName(f, i) {
    const ext = (f.type.split("/")[1] || "png").split("+")[0];
    const d = new Date();
    const p = (n) => String(n).padStart(2, "0");
    return `pasted-${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}-${p(d.getHours())}${p(d.getMinutes())}${p(d.getSeconds())}${i ? `-${i + 1}` : ""}.${ext}`;
  }

  // refuse says why a file will not be sent, or "".
  function refuse(f) {
    const ext = f.name.includes(".") ? f.name.split(".").pop().toLowerCase() : "";
    if (blocked.has(ext)) return "This file type is not allowed.";
    if (f.size > max()) return `Over the ${humanBytes(max())} limit.`;
    return "";
  }

  // Each drop or selection adds a queue row per file, in order. A refused
  // file gets a row too, failed, with the reason.
  function accept(picked) {
    for (const f of picked) {
      const it = queueBox.pbAdd({ name: f.name, size: f.size, file: f });
      const job = { file: f, it, order: ++picks, upload: {}, ctl: null, reason: refuse(f) };
      jobs.set(it, job);
      if (job.reason) it.fail(job.reason);
      else queue.push(job);
    }
    pump();
  }

  function pump() {
    while (running < 2 && queue.length) {
      const job = queue.shift();
      running++;
      run(job).finally(() => { running--; pump(); });
    }
  }

  // tidy takes the finished rows out of the queue once nothing is left to
  // send: their result rows are below, and the queue keeps only what needs
  // a decision (failed and canceled files).
  function tidy() {
    if (queueBox.pbActive() > 0) return;
    const finished = queueBox.pbItems().filter((i) => i.state === "done");
    if (!finished.length) return;
    for (const i of finished) { jobs.delete(i); i.remove(); }
    pbAnnounce(`${finished.length} ${finished.length === 1 ? "file" : "files"} uploaded`);
  }

  async function run(job) {
    const { file, it } = job;
    job.ctl = new AbortController();
    const t0 = performance.now();
    it.start();
    const progress = (sent) => {
      const secs = (performance.now() - t0) / 1000;
      it.progress(sent, secs > 0.5 ? sent / secs : 0);
    };
    try {
      const item = await uploadFile(file, { signal: job.ctl.signal, onProgress: progress, onProcessing: () => it.process(), state: job.upload });
      it.done();
      const thumb = file.type.startsWith("image/") ? URL.createObjectURL(file) : "";
      done.hidden = false;
      done.prepend(resultRow(item, thumb));
      addRecent(item);
      if (offers && offer.add(item, job.order)) showOffer(offers, offer);
    } catch (err) {
      // A canceled row is the kit's: Retry and Remove are on it.
      if (!err.canceled) it.fail(err.message);
    } finally {
      job.ctl = null;
      tidy();
    }
  }

  queueBox.addEventListener("pb:cancel", (e) => {
    const job = jobs.get(e.detail);
    if (!job) return;
    const i = queue.indexOf(job);
    if (i >= 0) queue.splice(i, 1);
    job.ctl?.abort();
    release(job.upload);
  });
  queueBox.addEventListener("pb:retry", (e) => {
    const job = jobs.get(e.detail);
    if (!job) return;
    job.reason = refuse(job.file);
    if (job.reason) {
      job.it.fail(job.reason);
      return;
    }
    queue.push(job);
    pump();
  });
  queueBox.addEventListener("pb:remove", (e) => { jobs.delete(e.detail); });
}

const drop = globalThis.document?.querySelector("#panel-upload [data-drop]");
if (drop) init(drop);
