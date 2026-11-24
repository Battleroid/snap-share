import { pbBusy, pbNoticeInto } from "../vendor/patchbay/patchbay.mjs";
import { init as initControls } from "./kit.js";
import { el, resultRow, addRecent } from "./item-rows.js";
import { setFieldError, clearFieldErrors, reid } from "./field-error.js";

// Paste and Link tabs of the composer.
//
// The Paste tab starts with one file block, which makes a paste. Add file
// appends more; with two or more the form gets a title and makes a gist.
// Both forms are kit forms (data-validate): an empty paste or link is caught
// on the field, and what the server refuses is put on the field it is about
// (fieldForError), or in one notice under the form when it is about neither.

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

// MESSAGES are what the kit's validation says under a field when the browser's
// own wording would not help.
const MESSAGES = {
  content: "Enter something to paste.",
  url: "Enter a link, like https://example.com.",
  slug: "Use letters, digits, - and _ for the name.",
};

// fieldForError is the field of a form (kind: "paste" or "link") that the
// server's error is about, by name, or "" when it is about none of them.
// The API answers with a message only, so this reads the words the handlers
// use (internal/items: link.go, options.go, paste.go).
export function fieldForError(kind, message) {
  const m = String(message || "");
  if (kind === "link") {
    if (/\b(URL|links?|host)\b/i.test(m)) return "url";
    if (/\bname\b/i.test(m)) return "slug";
  }
  if (kind === "paste" && /\bpaste is empty\b/i.test(m)) return "content";
  return "";
}

const optionsForm = globalThis.document?.getElementById("composer-options");

function readOptions() {
  const out = {};
  if (!optionsForm) return out;
  for (const [k, v] of new FormData(optionsForm)) if (v !== "") out[k] = String(v);
  return out;
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
// the form's fields are cleared and reset (if any) runs. kind says which
// fields an error can be about (fieldForError).
function wire(form, kind, request, reset) {
  if (!form) return;
  const result = form.querySelector("[data-result]");
  const button = form.querySelector("button[type=submit]");
  for (const [name, text] of Object.entries(MESSAGES)) {
    form.querySelectorAll(`[name="${name}"]`).forEach((c) => { c.dataset.error = text; });
  }
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    // The kit has marked what is missing and focused the first of it.
    if (e.pbInvalid?.length) return;
    clearFieldErrors(form);
    result.replaceChildren();
    const { url, body, done } = request();
    pbBusy(true, button);
    try {
      const item = await post(url, { ...readOptions(), ...body });
      result.replaceChildren(resultList(item, done));
      addRecent(item);
      form.querySelectorAll("input:not([type=hidden]),textarea").forEach((i) => { i.value = ""; });
      reset?.();
    } catch (err) {
      const name = fieldForError(kind, err.message);
      const control = name && [...form.querySelectorAll(`[name="${name}"]`)].find((c) => name !== "content" || !c.value.trim());
      if (control) {
        setFieldError(control, err.message);
        control.focus();
      } else {
        pbNoticeInto(result, { variant: "down", title: err.message });
      }
    } finally {
      pbBusy(false, button);
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

  // The kit's CodeArea keeps its highlight layer and line numbers in step with
  // the textarea (also after a script sets its value). The bar's file name and
  // language follow the two fields.
  const syncBar = (b) => {
    const bar = b.querySelector("[data-code-name]");
    if (bar) bar.textContent = field(b, "name").value.trim() || "untitled";
    const lang = b.querySelector("[data-code-lang]");
    if (lang) lang.textContent = field(b, "lang").value.trim() || "auto";
  };
  list.addEventListener("input", (e) => {
    const b = e.target.closest?.("[data-snippet]");
    if (b) syncBar(b);
  });
  // Choosing a language from the list fires change, not input.
  list.addEventListener("change", (e) => {
    const b = e.target.closest?.("[data-snippet]");
    if (b) syncBar(b);
  });
  list.addEventListener("click", () => {
    // The combobox sets its value on a pick; look again after it has.
    setTimeout(() => blocks().forEach(syncBar));
  });

  // A copy of the first block is a block of its own: every id (and what
  // points at it) gets a suffix, errors and the listbox's picked state are
  // dropped, and the kit binds the combobox and the CodeArea again.
  let copies = 1;
  add.addEventListener("click", () => {
    const all = blocks();
    if (all.length >= MAX_GIST_FILES) return;
    const b = all[0].cloneNode(true);
    b.querySelectorAll("input,textarea").forEach((i) => { i.value = ""; });
    clearFieldErrors(b);
    reid(b, ++copies);
    const lb = b.querySelector(".pb-listbox");
    const combo = b.querySelector("[role=combobox]");
    if (lb && combo) {
      lb.hidden = true;
      lb.querySelectorAll(".pb-option").forEach((o) => {
        o.removeAttribute("data-active");
        o.removeAttribute("aria-selected");
        if (!o.classList.contains("pb-option--empty")) { o.hidden = false; o.textContent = o.dataset.label || o.textContent; }
      });
      combo.setAttribute("aria-expanded", "false");
      combo.removeAttribute("aria-activedescendant");
    }
    list.append(b);
    syncBar(b);
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
      blocks().forEach(syncBar);
      sync();
    },
  };
}

if (globalThis.document) {
  const pasteForm = document.querySelector("[data-paste-form]");
  if (pasteForm) {
    const files = fileBlocks(pasteForm);
    wire(pasteForm, "paste", files.request, files.reset);
  }

  const linkForm = document.querySelector("[data-link-form]");
  wire(linkForm, "link", () => {
    const f = new FormData(linkForm);
    const body = { url: String(f.get("url") || "") };
    if (f.get("slug")) body.slug = String(f.get("slug"));
    return { url: "/api/v1/links", body, done: "Link created" };
  });

  optionsForm?.addEventListener("submit", (e) => e.preventDefault());
  const dropset = document.querySelector("[data-dropset]");
  if (optionsForm && dropset) wireDropset(dropset);
}

// defaultsBody is the Settings > Defaults form body for "Save as default":
// the composer's expires, visibility and file names, over the rest of the
// user's current defaults (that form replaces them all). Never the password.
export function defaultsBody(options, keep) {
  const body = new URLSearchParams(keep);
  body.set("naming", options.naming || "random");
  body.set("visibility", options.visibility || "unlisted");
  body.set("expiry", options.expires || "never");
  return body;
}

// wireDropset finishes the kit's upload settings: the password field only
// counts (is not disabled, so FormData sends it) while its switch is on, and
// Save as default posts to the Settings > Defaults handler.
function wireDropset(box) {
  const sw = box.querySelector("[data-dropset-reveal]");
  const pw = optionsForm.elements.password;
  const syncPassword = () => {
    pw.disabled = !sw.checked;
    if (!sw.checked) pw.value = "";
  };
  box.addEventListener("change", syncPassword);
  // The kit's Reset runs first; this runs after it has restored the switch.
  box.querySelector("[data-dropset-reset]")?.addEventListener("click", () => setTimeout(syncPassword));
  syncPassword();

  // The kit shows "Saving…" on Save as default and settles only on the
  // pb:saved or pb:error this sends back once the server has answered.
  box.addEventListener("pb:default", async () => {
    box.querySelector("[data-default-error]")?.remove();
    const keep = document.getElementById("composer-keep");
    try {
      const res = await fetch("/settings/defaults", {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/x-www-form-urlencoded" },
        body: defaultsBody(readOptions(), new FormData(keep)),
      });
      if (!res.ok) throw new Error("The defaults were not saved.");
      box.dispatchEvent(new CustomEvent("pb:saved"));
    } catch (err) {
      box.dispatchEvent(new CustomEvent("pb:error", { detail: { message: err.message } }));
      const holder = el("div");
      holder.dataset.defaultError = "";
      box.prepend(holder);
      pbNoticeInto(holder, { variant: "down", title: err.message });
    }
  });
}
