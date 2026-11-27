// Rename in place: a name with a pencil button ([data-rename], bound here)
// turns into a field with Save and Cancel. Save PATCHes {field: value} to
// data-rename-url (/api/v1/items/{id} or /api/v1/albums/{id}); Enter saves,
// Escape cancels. On success the element fires "app:renamed" with the
// server's item as detail; on failure the API's message shows under the
// field, which stays open. A file name starts with its stem selected, so
// typing keeps the extension.
//
// Markup (owner pages only; templates/_partials/item-edit.html item.rename):
//   <div data-rename data-rename-url="…" data-rename-field="title|filename"
//        data-rename-value="raw value" data-rename-label="title"
//        [data-rename-show="name"]>
//     <span data-rename-text>shown value</span>
//     <button type="button" data-rename-start aria-label="Rename …">…</button>
//   </div>
// startRename(host, opts) opens the same field over any element (the album
// page's tile menu uses it).

import { api } from "./api.js";
import { stemLength } from "./item-edit.js";

const limits = { title: 200, filename: 255 };

function el(tag, cls, text) {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text != null) n.textContent = text;
  return n;
}

async function patch(url, field, value) {
  const res = await api("PATCH", url, { [field]: value });
  return res.ok ? { data: res.data || {} } : { error: res.error };
}

// startRename replaces the children of hide (default: host's children) with
// a rename field inside host until it is saved or cancelled. opts: url,
// field, value, label (for the field's accessible name), onSaved(data).
export function startRename(host, { url, field, value, label, onSaved, hide }) {
  if (host.querySelector(":scope > [data-rename-form]")) return;
  const hidden = hide || [...host.children];
  for (const n of hidden) n.hidden = true;

  const form = el("form", "app-rename__form");
  form.dataset.renameForm = "";
  form.noValidate = true;
  const wrap = el("span", "pb-input app-rename__input");
  const input = el("input");
  Object.assign(input, { type: "text", name: field, value: value || "", maxLength: limits[field] || 200, autocomplete: "off", spellcheck: false });
  input.setAttribute("aria-label", label || field);
  if (field === "title") input.placeholder = "Untitled";
  wrap.append(input);
  const save = el("button", "pb-btn pb-btn--primary pb-btn--sm", "Save");
  save.type = "submit";
  const cancel = el("button", "pb-btn pb-btn--ghost pb-btn--sm", "Cancel");
  cancel.type = "button";
  const err = el("p", "pb-field__error app-rename__error");
  err.id = `rename-error-${Math.random().toString(36).slice(2)}`;
  err.hidden = true;
  err.setAttribute("role", "alert");
  form.append(wrap, save, cancel, err);
  host.append(form);

  const close = () => {
    form.remove();
    for (const n of hidden) n.hidden = false;
    host.querySelector("[data-rename-start]")?.focus();
  };
  const fail = (msg) => {
    err.textContent = msg;
    err.hidden = false;
    input.setAttribute("aria-invalid", "true");
    input.setAttribute("aria-describedby", err.id);
    input.focus();
  };

  cancel.addEventListener("click", close);
  form.addEventListener("keydown", (e) => {
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      close();
    }
  });
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    if (save.disabled) return;
    const next = field === "title" ? input.value : input.value.trim();
    if (field === "filename" && !next) return fail("Enter a file name.");
    if (next === (value || "")) return close();
    save.disabled = true;
    save.setAttribute("aria-busy", "true");
    const { data, error } = await patch(url, field, next);
    save.disabled = false;
    save.removeAttribute("aria-busy");
    if (error) return fail(error);
    form.remove();
    for (const n of hidden) n.hidden = false;
    onSaved?.(data);
  });

  input.focus();
  if (field === "filename") input.setSelectionRange(0, stemLength(input.value));
  else input.select();
}

document.addEventListener("click", (e) => {
  const b = e.target instanceof Element ? e.target.closest("[data-rename-start]") : null;
  const host = b?.closest("[data-rename]");
  if (!host) return;
  const d = host.dataset;
  startRename(host, {
    url: d.renameUrl,
    field: d.renameField,
    value: d.renameValue,
    label: d.renameLabel,
    onSaved: (data) => {
      const v = data[d.renameField] ?? "";
      d.renameValue = v;
      const text = host.querySelector("[data-rename-text]");
      // data-rename-show="name": show what the item is called (its title,
      // else its file name or link), as an album's heading does.
      if (text) text.textContent = (d.renameShow === "name" ? data.name : v) || "—";
      host.dispatchEvent(new CustomEvent("app:renamed", { bubbles: true, detail: data }));
      host.querySelector("[data-rename-start]")?.focus();
    },
  });
});
