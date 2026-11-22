import { init as initControls } from "./patchbay.js";

// Paste and Link tabs of the composer.
//
// The Paste tab starts with one file block, which makes a paste. Add file
// appends more; with two or more the form gets a title and makes a gist.

// MAX_GIST_FILES is the server's cap on files in one gist.
export const MAX_GIST_FILES = 50;

// pasteBody is the POST /api/v1/pastes body for one file block.
export function pasteBody(file) {
  const body = { content: file.content || "" };
  for (const k of ["name", "lang"]) if (file[k]) body[k] = file[k];
  return body;
}

// gistBody is the POST /api/v1/gists body for several file blocks, in order.
export function gistBody(title, files) {
  const body = {};
  const t = String(title || "").trim();
  if (t) body.title = t;
  body.files = files.map((f) => ({ name: f.name || "", lang: f.lang || "", content: f.content || "" }));
  return body;
}

// pasteRequest is what the Paste tab sends: one block is a paste, more a gist.
export function pasteRequest(title, files) {
  if (files.length > 1) return { url: "/api/v1/gists", body: gistBody(title, files), done: "Gist created" };
  return { url: "/api/v1/pastes", body: pasteBody(files[0] || {}), done: "Paste created" };
}

// snippetState is how the Paste tab looks with n file blocks.
export function snippetState(n) {
  const gist = n > 1;
  return { gist, label: gist ? "Create gist" : "Create paste", canAdd: n < MAX_GIST_FILES };
}

const optionsForm = globalThis.document?.getElementById("composer-options");

function readOptions() {
  const out = {};
  if (!optionsForm) return out;
  for (const [k, v] of new FormData(optionsForm)) if (v !== "") out[k] = String(v);
  return out;
}

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

function notice(status, title, url) {
  const n = el("div", "pb-notice");
  if (status) n.dataset.status = status;
  n.setAttribute("role", status === "down" ? "alert" : "status");
  const ic = el("span", `pb-icon pb-icon--status-${status || "info"}`);
  ic.setAttribute("aria-hidden", "true");
  n.append(ic, el("p", "pb-notice__title", title));
  if (url) {
    const body = el("p", "pb-notice__body");
    const a = el("a", "pb-link pb-mono", url.replace(/^https?:\/\//, ""));
    a.href = url;
    const b = el("button", "pb-btn pb-btn--ghost pb-btn--icon pb-btn--sm");
    b.type = "button";
    b.dataset.copy = "";
    b.dataset.copyValue = url;
    b.setAttribute("aria-label", "Copy link");
    b.append(el("span", "pb-icon pb-icon--copy"));
    body.append(a, " ", b);
    n.append(body);
  }
  return n;
}

function addRecent(item) {
  const list = document.querySelector("[data-recent]");
  if (!list) return;
  list.querySelector("[data-recent-empty]")?.remove();
  const row = el("div", "pb-filerow");
  const ic = el("span", `pb-icon pb-icon--${{ link: "link", gist: "terminal" }[item.kind] || "file"}`);
  ic.setAttribute("aria-hidden", "true");
  const a = el("a", "pb-link pb-link--quiet", item.name);
  a.href = item.page_url;
  row.append(ic, a, el("span", "pb-filerow__meta", "just now"));
  list.prepend(row);
}

async function post(url, body) {
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify(body),
    credentials: "same-origin",
  });
  let data = null;
  try { data = await res.json(); } catch { /* empty */ }
  if (!res.ok) throw new Error((data && data.error) || `Request failed (${res.status}).`);
  return data;
}

// wire submits form as JSON: request() gives { url, body, done }. On success
// the form's fields are cleared and reset (if any) runs.
function wire(form, request, reset) {
  if (!form) return;
  const result = form.querySelector("[data-result]");
  const button = form.querySelector("button[type=submit]");
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const { url, body, done } = request();
    button.setAttribute("aria-busy", "true");
    button.disabled = true;
    try {
      const item = await post(url, { ...readOptions(), ...body });
      result.replaceChildren(notice("ok", done, item.url));
      addRecent(item);
      form.querySelectorAll("input:not([type=hidden]),textarea").forEach((i) => { i.value = ""; });
      reset?.();
    } catch (err) {
      result.replaceChildren(notice("down", err.message));
    } finally {
      button.removeAttribute("aria-busy");
      button.disabled = false;
    }
  });
}

function setLabel(button, text) {
  const t = [...button.childNodes].reverse().find((n) => n.nodeType === Node.TEXT_NODE && n.textContent.trim());
  if (t) t.textContent = text;
}

// fileBlocks runs the Paste tab's file blocks: Add file, the remove buttons,
// and the title and submit label that follow the count.
function fileBlocks(form) {
  const list = form.querySelector("[data-snippets]");
  const title = form.querySelector("[data-gist-title]");
  const add = form.querySelector("[data-snippet-add]");
  const submit = form.querySelector("button[type=submit]");
  const blocks = () => [...list.querySelectorAll("[data-snippet]")];
  const field = (b, name) => b.querySelector(`[name="${name}"]`);

  function sync() {
    const all = blocks();
    const s = snippetState(all.length);
    title.hidden = !s.gist;
    all.forEach((b, i) => {
      b.classList.toggle("pb-fieldset", s.gist);
      const legend = b.querySelector("legend");
      legend.hidden = !s.gist;
      legend.textContent = `file ${i + 1}`;
      const rm = b.querySelector("[data-snippet-remove]");
      rm.hidden = !s.gist;
      rm.setAttribute("aria-label", `Remove file ${i + 1}`);
    });
    add.disabled = !s.canAdd;
    setLabel(submit, s.label);
  }

  // A copy of the first block gets its own language listbox: a fresh id, its
  // options reset, and the combobox started again (patchbay.js).
  let copies = 1;
  add.addEventListener("click", () => {
    const all = blocks();
    if (all.length >= MAX_GIST_FILES) return;
    const b = all[0].cloneNode(true);
    b.querySelectorAll("input,textarea").forEach((i) => { i.value = ""; });
    b.querySelectorAll("[data-pb-ready]").forEach((c) => { delete c.dataset.pbReady; });
    const lb = b.querySelector(".pb-listbox");
    const combo = b.querySelector("[role=combobox]");
    if (lb && combo) {
      lb.id = `composer-langs-${++copies}`;
      lb.hidden = true;
      lb.querySelectorAll(".pb-option").forEach((o) => {
        o.removeAttribute("id");
        o.removeAttribute("data-active");
        o.removeAttribute("aria-selected");
        if (!o.classList.contains("pb-option--empty")) { o.hidden = false; o.textContent = o.dataset.label || o.textContent; }
      });
      combo.setAttribute("aria-controls", lb.id);
      combo.setAttribute("aria-expanded", "false");
      combo.removeAttribute("aria-activedescendant");
    }
    list.append(b);
    initControls(b);
    sync();
    field(b, "name").focus();
  });

  list.addEventListener("click", (e) => {
    const rm = e.target.closest("[data-snippet-remove]");
    if (!rm) return;
    const all = blocks();
    if (all.length < 2) return;
    const i = all.indexOf(rm.closest("[data-snippet]"));
    all[i].remove();
    sync();
    const rest = blocks();
    field(rest[Math.min(i, rest.length - 1)], "name").focus();
  });

  sync();
  return {
    request: () => pasteRequest(
      field(title, "title").value,
      blocks().map((b) => ({ name: field(b, "name").value, lang: field(b, "lang").value, content: field(b, "content").value })),
    ),
    reset() {
      blocks().slice(1).forEach((b) => b.remove());
      sync();
    },
  };
}

if (globalThis.document) {
  const pasteForm = document.querySelector("[data-paste-form]");
  if (pasteForm) {
    const files = fileBlocks(pasteForm);
    wire(pasteForm, files.request, files.reset);
  }

  const linkForm = document.querySelector("[data-link-form]");
  wire(linkForm, () => {
    const f = new FormData(linkForm);
    const body = { url: String(f.get("url") || "") };
    if (f.get("slug")) body.slug = String(f.get("slug"));
    return { url: "/api/v1/links", body, done: "Link created" };
  });

  optionsForm?.addEventListener("submit", (e) => e.preventDefault());
}
