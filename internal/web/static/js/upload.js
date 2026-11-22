// DropZone and resumable uploads for the composer.
//
// The whole window is the drop target; an enter/leave counter keeps the
// "over" state steady while the pointer crosses child elements. Each file is
// declared (POST /api/v1/uploads), sent in parts (PUT ?offset=, resuming from
// the server's offset after a 409 or a dropped connection) and completed.
// Progress is drawn by setting --value on the row's pb-progress.

const drop = document.querySelector("[data-drop]");
if (drop) init(drop);

export function humanBytes(n) {
  if (n < 1000) return `${n} B`;
  const units = "KMGTPE";
  let v = n / 1000;
  let i = 0;
  while (v >= 1000 && i < units.length - 1) { v /= 1000; i++; }
  return `${v >= 100 ? v.toFixed(0) : v.toFixed(1)} ${units[i]}B`;
}

function readOptions() {
  const form = document.getElementById("composer-options");
  const out = {};
  if (!form) return out;
  for (const [k, v] of new FormData(form)) if (v !== "") out[k] = String(v);
  return out;
}

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
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

// addRecent puts a finished item at the top of the composer's recent list.
export function addRecent(item) {
  const list = document.querySelector("[data-recent]");
  if (!list) return;
  list.querySelector("[data-recent-empty]")?.remove();
  const row = el("div", "pb-filerow");
  row.append(el("span", `pb-icon pb-icon--${item.kind === "link" ? "link" : "file"}`));
  row.firstChild.setAttribute("aria-hidden", "true");
  const a = el("a", "pb-link pb-link--quiet", item.name);
  a.href = item.page_url;
  row.append(a, el("span", "pb-filerow__meta", item.kind === "link" ? "just now" : `${humanBytes(item.size)} · just now`));
  list.prepend(row);
}

// linkMeta is the "link + copy" content shown when something is created.
export function linkMeta(url) {
  const frag = document.createDocumentFragment();
  const a = el("a", "pb-link pb-mono", url.replace(/^https?:\/\//, ""));
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

function init(zone) {
  const input = zone.querySelector("input[type=file]");
  const icon = zone.querySelector(".pb-drop__icon");
  const title = zone.querySelector(".pb-drop__title");
  const hint = zone.querySelector(".pb-drop__hint");
  const list = document.querySelector("[data-filelist]");
  const idleTitle = title.textContent;
  const idleHint = [...hint.childNodes].map((n) => n.cloneNode(true));
  const max = () => Number(zone.dataset.max) || Infinity;
  const blocked = new Set((zone.dataset.blocked || "").split(",").filter(Boolean));
  let depth = 0;

  const setIcon = (name) => { icon.className = `pb-icon pb-icon--${name} pb-drop__icon`; };
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

  // While the composer's Secret tab is open its own drop zone takes files;
  // the window-wide upload target must never catch them (they would be
  // uploaded unencrypted).
  const secretPanel = document.getElementById("panel-secret");
  const secretOpen = () => !!secretPanel && !secretPanel.hidden;
  const hasFiles = (e) => !secretOpen() && [...(e.dataTransfer?.types || [])].includes("Files");
  const count = (e) => [...(e.dataTransfer?.items || [])].filter((i) => i.kind === "file").length;

  window.addEventListener("dragenter", (e) => {
    if (!hasFiles(e)) return;
    e.preventDefault();
    if (depth++ === 0) {
      document.getElementById("tab-upload")?.click();
      over(count(e));
    }
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

  const queue = [];
  let running = 0;

  function accept(files) {
    const rejected = [];
    for (const f of files) {
      const ext = f.name.includes(".") ? f.name.split(".").pop().toLowerCase() : "";
      if (blocked.has(ext)) rejected.push(`${f.name}: this file type is not allowed`);
      else if (f.size > max()) rejected.push(`${f.name}: over the ${humanBytes(max())} limit`);
      else queue.push({ file: f, row: addRow(f) });
    }
    if (rejected.length) error(rejected.join(" · "));
    pump();
  }

  function pump() {
    while (running < 2 && queue.length) {
      const job = queue.shift();
      running++;
      run(job).finally(() => { running--; pump(); });
    }
  }

  function addRow(file) {
    const row = el("div", "pb-filerow");
    const ic = el("span", "pb-icon pb-icon--file");
    ic.setAttribute("aria-hidden", "true");
    const meta = el("span", "pb-filerow__meta", `${humanBytes(file.size)} · waiting`);
    const bar = el("div", "pb-progress");
    bar.append(el("span"));
    bar.setAttribute("role", "progressbar");
    bar.setAttribute("aria-label", file.name);
    bar.setAttribute("aria-valuemin", "0");
    bar.setAttribute("aria-valuemax", "100");
    row.append(ic, el("span", "", file.name), meta, bar);
    list.prepend(row);
    return { row, ic, meta, bar };
  }

  async function run({ file, row }) {
    const progress = (sent, total) => {
      const pct = total > 0 ? Math.floor((sent / total) * 100) : 100;
      row.bar.style.setProperty("--value", `${pct}%`);
      row.bar.setAttribute("aria-valuenow", String(pct));
      row.meta.textContent = `${humanBytes(file.size)} · ${pct}%`;
    };
    progress(0, file.size);
    try {
      const item = await uploadFile(file, progress);
      row.bar.remove();
      row.row.classList.add("app-done");
      row.ic.className = "pb-icon pb-icon--status-ok";
      row.meta.replaceChildren(linkMeta(item.url));
      addRecent(item);
    } catch (err) {
      row.bar.remove();
      row.row.classList.add("app-failed");
      row.ic.className = "pb-icon pb-icon--status-down";
      row.meta.textContent = err.message;
      if (err.status === 413 || err.status === 415 || err.status === 507) error(`${file.name}: ${err.message}`);
    }
  }
}
