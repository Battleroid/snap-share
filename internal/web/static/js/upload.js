// DropZone and resumable uploads for the composer.
//
// While the Upload tab is open the whole page is the drop target (an
// enter/leave counter keeps the "over" state steady while the pointer crosses
// child elements), and an image pasted from the clipboard is uploaded too.
// Each file is declared (POST /api/v1/uploads), sent in parts (PUT ?offset=,
// resuming from the server's offset after a 409 or a dropped connection) and
// completed. Progress shows in kit UploadQueue rows (snap has no pause or
// cancel); a finished file leaves the queue as a result row with its link.
// When two or more files of one drop or selection succeed, the panel offers
// to share them as one album.

import { el, icon, humanBytes, notice, resultRow, addRecent, shortLink } from "./item-rows.js";

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

async function api(method, url, body) {
  const res = await fetch(url, {
    method,
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: "same-origin",
  });
  let data = null;
  try { data = await res.json(); } catch { /* empty body */ }
  if (!res.ok) throw new HTTPError(res.status, data);
  return data;
}

function putPart(id, offset, blob, onProgress) {
  return new Promise((resolve, reject) => {
    const x = new XMLHttpRequest();
    x.open("PUT", `/api/v1/uploads/${encodeURIComponent(id)}?offset=${offset}`);
    x.responseType = "json";
    x.setRequestHeader("Accept", "application/json");
    x.upload.addEventListener("progress", (e) => onProgress(e.loaded));
    x.addEventListener("load", () => (x.status >= 200 && x.status < 300 ? resolve(x.response) : reject(new HTTPError(x.status, x.response))));
    x.addEventListener("error", () => reject(Object.assign(new Error("Connection lost."), { retry: true })));
    x.addEventListener("timeout", () => reject(Object.assign(new Error("Timed out."), { retry: true })));
    x.send(blob);
  });
}

async function serverOffset(id) {
  const res = await fetch(`/api/v1/uploads/${encodeURIComponent(id)}`, { method: "HEAD", credentials: "same-origin" });
  if (!res.ok) throw new HTTPError(res.status, null);
  return Number(res.headers.get("Upload-Offset")) || 0;
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// uploadFile runs the declare / parts / complete sequence for one file.
export async function uploadFile(file, onProgress, options = readOptions()) {
  const init = await api("POST", "/api/v1/uploads", { name: file.name, size: file.size, type: file.type, options });
  const part = init.part_size || 8 << 20;
  let offset = 0;
  let failures = 0;
  while (offset < file.size) {
    const end = Math.min(offset + part, file.size);
    try {
      const start = offset;
      const res = await putPart(init.id, offset, file.slice(offset, end), (loaded) => onProgress(start + loaded, file.size));
      offset = res.offset;
      failures = 0;
    } catch (err) {
      if (err.status === 409 && err.body && typeof err.body.offset === "number") {
        offset = err.body.offset;
        continue;
      }
      if ((err.retry || err.status >= 500) && failures < 6) {
        failures++;
        await sleep(Math.min(500 * 2 ** failures, 15000));
        try { offset = await serverOffset(init.id); } catch { /* retry the same offset */ }
        continue;
      }
      throw err;
    }
  }
  onProgress(file.size, file.size);
  return api("POST", `/api/v1/uploads/${encodeURIComponent(init.id)}/complete`);
}

// newBatch tracks the files of one drop or selection. settle(i, item)
// records file i's result (its item, or null when it failed) and returns true
// when that settled the last one; items() lists the uploaded items in the
// order the files were picked.
export function newBatch(n) {
  const results = new Array(n).fill(undefined);
  let left = n;
  return {
    settle(i, item) {
      if (!(i >= 0 && i < n) || results[i] !== undefined) return false;
      results[i] = item || null;
      return --left === 0;
    },
    get done() { return left === 0; },
    items: () => results.filter(Boolean),
  };
}

// albumBody is the POST /api/v1/albums body for a batch: the composer's
// options, the title when one is given, and the items' ids in order.
export function albumBody(title, items, options = {}) {
  const body = { ...options };
  const t = String(title || "").trim();
  if (t) body.title = t;
  body.items = items.map((i) => i.id);
  return body;
}

// queueTitle is the UploadQueue's heading and its small line: how many files
// are still going, and how far the batch has come.
export function queueTitle({ waiting, failed, sent, total, perSec }) {
  const noun = (n) => `${n} ${n === 1 ? "file" : "files"}`;
  if (waiting > 0) {
    const bits = [`${humanBytes(sent)} of ${humanBytes(total)}`];
    if (perSec > 0) bits.push(`${humanBytes(perSec)}/s`);
    return { title: `Uploading ${noun(waiting)}`, small: bits.join(" · ") };
  }
  if (failed > 0) return { title: `${failed} ${failed === 1 ? "upload" : "uploads"} failed`, small: "" };
  return { title: "", small: "" };
}

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

// offerAlbum prepends the "N files uploaded" prompt for a finished batch to
// box. Creating the album replaces it with the album's link; an error leaves
// it in place.
function offerAlbum(box, items) {
  const wrap = el("div", "app-stack");
  const prompt = notice("", `${items.length} files uploaded`, "Share them together as one album, with one link.");
  const close = el("button", "pb-btn pb-btn--ghost pb-btn--icon pb-btn--sm");
  close.type = "button";
  close.setAttribute("aria-label", "Dismiss");
  close.append(icon("close"));
  close.addEventListener("click", () => wrap.remove());

  const form = el("form", "pb-notice__actions app-album-offer");
  form.method = "dialog";
  const input = el("input");
  Object.assign(input, { name: "title", type: "text", maxLength: 200, placeholder: "Album title (optional)", autocomplete: "off" });
  input.setAttribute("aria-label", "Album title");
  const field = el("span", "pb-input app-album-offer__title");
  field.append(input);
  const create = el("button", "pb-btn pb-btn--sm pb-btn--primary");
  create.type = "submit";
  create.append(icon("folder"), "Create album");
  form.append(field, create);
  prompt.append(form, close);

  const result = el("div");
  wrap.append(prompt, result);
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    create.setAttribute("aria-busy", "true");
    create.disabled = true;
    try {
      const album = await api("POST", "/api/v1/albums", albumBody(input.value, items, readOptions()));
      const ok = notice("ok", "Album created");
      const body = el("p", "pb-notice__body");
      body.append(linkMeta(album.url));
      ok.append(body);
      wrap.replaceChildren(ok);
      addRecent(album);
    } catch (err) {
      result.replaceChildren(notice("down", err.message));
    } finally {
      create.removeAttribute("aria-busy");
      create.disabled = false;
    }
  });
  box.prepend(wrap);
}

function init(zone) {
  const input = zone.querySelector("input[type=file]");
  const icn = zone.querySelector(".pb-drop__icon");
  const title = zone.querySelector(".pb-drop__title");
  const hint = zone.querySelector(".pb-drop__hint");
  const files = document.querySelector("[data-filelist]");
  const queueBox = files.querySelector("[data-queue]");
  const queueList = files.querySelector("[data-queue-list]");
  const queueHead = files.querySelector("[data-queue-title]");
  const queueTotal = files.querySelector("[data-queue-total]");
  const done = files.querySelector("[data-uploaded]");
  const offers = document.querySelector("[data-album-offers]");
  const idleTitle = title.textContent;
  const idleHint = [...hint.childNodes].map((n) => n.cloneNode(true));
  const max = () => Number(zone.dataset.max) || Infinity;
  const blocked = new Set((zone.dataset.blocked || "").split(",").filter(Boolean));
  let depth = 0;

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
  const error = (msg) => {
    zone.dataset.state = "error";
    setIcon("upload");
    title.textContent = idleTitle;
    hint.textContent = msg;
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

  const queue = [];
  let running = 0;
  // The batch shown in the queue: its bytes and when it started, for the speed.
  let session = null;

  function pastedName(f, i) {
    const ext = (f.type.split("/")[1] || "png").split("+")[0];
    const d = new Date();
    const p = (n) => String(n).padStart(2, "0");
    return `pasted-${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}-${p(d.getHours())}${p(d.getMinutes())}${p(d.getSeconds())}${i ? `-${i + 1}` : ""}.${ext}`;
  }

  // Each drop or selection is a batch; its accepted files are queued in order.
  function accept(picked) {
    const rejected = [];
    const ok = [];
    for (const f of picked) {
      const ext = f.name.includes(".") ? f.name.split(".").pop().toLowerCase() : "";
      if (blocked.has(ext)) rejected.push(`${f.name}: this file type is not allowed`);
      else if (f.size > max()) rejected.push(`${f.name}: over the ${humanBytes(max())} limit`);
      else ok.push(f);
    }
    const batch = newBatch(ok.length);
    if (ok.length && !session) session = { total: 0, sent: new Map(), start: Date.now() };
    ok.forEach((f, index) => {
      session.total += f.size;
      queue.push({ file: f, row: addRow(f), batch, index });
    });
    if (rejected.length) error(rejected.join(" · "));
    drawQueue();
    pump();
  }

  // settle records a file's result; the last one of a batch with two or more
  // uploaded items offers the album.
  function settle({ batch, index }, item) {
    if (!batch.settle(index, item)) return;
    const items = batch.items();
    if (items.length >= 2 && offers) offerAlbum(offers, items);
  }

  function pump() {
    while (running < 2 && queue.length) {
      const job = queue.shift();
      running++;
      run(job).finally(() => { running--; pump(); });
    }
  }

  // drawQueue writes the queue's heading and total bar from the rows.
  function drawQueue() {
    const rows = [...queueList.children];
    queueBox.hidden = rows.length === 0;
    const waiting = rows.filter((r) => r.dataset.state !== "failed").length;
    const failed = rows.length - waiting;
    let sent = 0;
    if (session) for (const n of session.sent.values()) sent += n;
    const secs = session ? (Date.now() - session.start) / 1000 : 0;
    const total = session ? session.total : 0;
    const t = queueTitle({ waiting, failed, sent, total, perSec: secs > 0.5 ? sent / secs : 0 });
    queueHead.textContent = t.title;
    if (t.small) queueHead.append(el("small", "", t.small));
    queueTotal.style.setProperty("--p", `${total > 0 ? Math.min(100, Math.floor((sent / total) * 100)) : 0}%`);
    if (waiting === 0) session = null;
  }

  function addRow(file) {
    const row = el("li", "pb-upq__item");
    row.dataset.state = "queued";
    const ic = icon("clock");
    const name = el("span", "pb-upq__name", file.name);
    name.title = file.name;
    const meta = el("span", "pb-upq__meta");
    const bar = el("span", "pb-upq__bar");
    const fill = el("span");
    fill.style.setProperty("--p", "0%");
    bar.append(fill);
    bar.setAttribute("role", "progressbar");
    bar.setAttribute("aria-label", file.name);
    bar.setAttribute("aria-valuemin", "0");
    bar.setAttribute("aria-valuemax", "100");
    const txt = el("span", "pb-upq__txt", `${humanBytes(file.size)} · waiting`);
    meta.append(bar, txt);
    row.append(ic, name, meta);
    queueList.prepend(row);
    return { row, ic, fill, bar, txt };
  }

  async function run(job) {
    const { file, row } = job;
    const key = job;
    row.row.dataset.state = "uploading";
    row.ic.className = "pb-icon pb-icon--upload";
    const progress = (sent, total) => {
      const pct = total > 0 ? Math.floor((sent / total) * 100) : 100;
      row.fill.style.setProperty("--p", `${pct}%`);
      row.bar.setAttribute("aria-valuenow", String(pct));
      row.txt.textContent = `${humanBytes(sent)} / ${humanBytes(total)}`;
      session?.sent.set(key, sent);
      drawQueue();
    };
    progress(0, file.size);
    let item = null;
    try {
      item = await uploadFile(file, progress);
      const thumb = file.type.startsWith("image/") ? URL.createObjectURL(file) : "";
      row.row.remove();
      done.hidden = false;
      done.prepend(resultRow(item, thumb));
      addRecent(item);
    } catch (err) {
      item = null;
      row.row.dataset.state = "failed";
      row.ic.className = "pb-icon pb-icon--status-down";
      row.bar.remove();
      row.txt.textContent = err.message;
      row.txt.title = err.message;
      if (err.status === 413 || err.status === 415 || err.status === 507) error(`${file.name}: ${err.message}`);
    }
    drawQueue();
    settle(job, item);
  }
}

const drop = globalThis.document?.querySelector("#panel-upload [data-drop]");
if (drop) init(drop);
