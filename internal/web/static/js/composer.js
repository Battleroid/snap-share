import { init as initControls } from "./kit.js";
import { el, resultRow, addRecent } from "./item-rows.js";

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

// optionsSummary is the one line over the panels that says what the options
// will do: "expires 7d · unlisted · random name · no password". The password
// itself is never shown.
export function optionsSummary(o = {}) {
  const naming = { words: "word name", original: "original name" }[o.naming] || "random name";
  const expires = !o.expires || o.expires === "never" ? "never expires" : `expires ${o.expires}`;
  return [expires, o.visibility || "unlisted", naming, o.password ? "password set" : "no password"].join(" · ");
}

// resultList is what a created paste, gist or link shows under its form: a
// result row with the link, and the outcome for screen readers.
export function resultList(item, done) {
  const box = el("div", "app-stack");
  box.append(el("p", "pb-sr", done));
  box.lastChild.setAttribute("role", "status");
  const list = el("ul", "pb-list");
  list.setAttribute("aria-label", done);
  list.append(resultRow(item));
  box.append(list);
  return box;
}

const optionsForm = globalThis.document?.getElementById("composer-options");

function readOptions() {
  const out = {};
  if (!optionsForm) return out;
  for (const [k, v] of new FormData(optionsForm)) if (v !== "") out[k] = String(v);
  return out;
}

function errorNotice(title) {
  const n = el("div", "pb-notice");
  n.dataset.status = "down";
  n.setAttribute("role", "alert");
  const ic = el("span", "pb-icon pb-icon--status-down");
  ic.setAttribute("aria-hidden", "true");
  n.append(ic, el("p", "pb-notice__title", title));
  return n;
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
      result.replaceChildren(resultList(item, done));
      addRecent(item);
      form.querySelectorAll("input:not([type=hidden]),textarea").forEach((i) => { i.value = ""; });
      reset?.();
    } catch (err) {
      result.replaceChildren(errorNotice(err.message));
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

  // The CodeArea is a textarea over a highlight layer; there is no client-side
  // highlighter, so the layer shows the text as typed and the gutter its lines.
  const mirror = (b) => {
    const t = field(b, "content");
    const hl = b.querySelector(".pb-code__hl");
    const gutter = b.querySelector(".pb-code__gutter");
    if (hl) hl.textContent = t.value + "\n";
    if (gutter) {
      const n = Math.max(1, t.value.split("\n").length);
      gutter.textContent = Array.from({ length: n }, (_, i) => i + 1).join("\n");
    }
    const bar = b.querySelector("[data-code-name]");
    if (bar) bar.textContent = field(b, "name").value.trim() || "untitled";
    const lang = b.querySelector("[data-code-lang]");
    if (lang) lang.textContent = field(b, "lang").value.trim() || "auto";
  };
  list.addEventListener("input", (e) => {
    const b = e.target.closest?.("[data-snippet]");
    if (b) mirror(b);
  });
  // Choosing a language from the list fires change, not input.
  list.addEventListener("change", (e) => {
    const b = e.target.closest?.("[data-snippet]");
    if (b) mirror(b);
  });
  list.addEventListener("click", () => {
    // The combobox sets its value on a pick; look again after it has.
    setTimeout(() => blocks().forEach(mirror));
  });

  // A copy of the first block gets its own language listbox: a fresh id, its
  // options reset, and the combobox bound again (the kit, through kit.js).
  let copies = 1;
  add.addEventListener("click", () => {
    const all = blocks();
    if (all.length >= MAX_GIST_FILES) return;
    const b = all[0].cloneNode(true);
    b.querySelectorAll("input,textarea").forEach((i) => { i.value = ""; });
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
    mirror(b);
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
      blocks().forEach(mirror);
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
  const summary = document.querySelector("[data-options-summary]");
  if (optionsForm && summary) {
    const draw = () => { summary.textContent = optionsSummary(readOptions()); };
    optionsForm.addEventListener("input", draw);
    optionsForm.addEventListener("change", draw);
    draw();
  }
}
