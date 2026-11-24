// The Edit drawer for one item (_partials/item-edit.html), used by the
// dashboard and the item page. openEdit fills it from the item's JSON
// (id name title slug kind visibility password expires_at tags); Save
// PATCHes /api/v1/items/{id} with only what changed, then PUTs the tags if
// they changed, and calls onSaved once both are done. Save is busy while it
// works and cannot be sent twice. Errors stay in the drawer: if the PATCH
// went through and the tags did not, the drawer says exactly that, keeps what
// was saved as its new starting point (a second Save sends only the tags) and
// calls onSaved(item, { partial: true }) when it is closed, so the page shows
// what did change.

import { pbBusy, pbNoticeInto } from "../vendor/patchbay/patchbay.mjs";
import { api } from "./api.js";

const $ = (sel, root = document) => root.querySelector(sel);

export const splitTags = (s) => s.split(/[\n,;]+/).map((x) => x.trim()).filter(Boolean);

const UNITS = [["year", 31536e6], ["month", 2592e6], ["week", 6048e5], ["day", 864e5], ["hour", 36e5], ["minute", 6e4]];

// relativeTime says when iso is, in words ("in 3 days", "in 2 hours",
// "yesterday"), for the locale of the page.
export function relativeTime(iso, now = Date.now(), locale) {
  const diff = new Date(iso).getTime() - now;
  const rtf = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
  for (const [unit, ms] of UNITS) {
    if (Math.abs(diff) >= ms) return rtf.format(Math.round(diff / ms), unit);
  }
  return rtf.format(Math.round(diff / 1000), "second");
}

// keepLabel is the drawer's "leave the expiry as it is" choice.
export function keepLabel(expiresAt, now = Date.now(), locale) {
  return expiresAt ? `keep (${relativeTime(expiresAt, now, locale)})` : "keep (never)";
}

let editing = null; // { item, onSaved, changed, saving }

function showError(dlg, msg) {
  const box = $("[data-error]", dlg);
  if (!box) return;
  pbNoticeInto(box, { variant: "down", title: msg });
  box.hidden = false;
}

function hideError(dlg) {
  const box = $("[data-error]", dlg);
  if (!box) return;
  box.hidden = true;
  box.replaceChildren();
}

export function openEdit(item, onSaved) {
  const dlg = document.getElementById("item-edit");
  if (!dlg) return;
  const form = $("form", dlg);
  form.reset();
  hideError(dlg);
  editing = { item, onSaved, changed: false, saving: false };
  fill(dlg, form, item);
  dlg.showModal();
}

// fill puts the item's values in the drawer.
function fill(dlg, form, item) {
  const el = form.elements;
  $(".pb-dialog__title", dlg).textContent = `Edit ${item.name}`;
  $(".pb-dialog__sub", dlg).textContent = `${location.host}/${item.slug}`;
  el.title.value = item.title;
  el.slug.value = item.slug;
  el.visibility.value = item.visibility;
  el.expires.value = "";
  $("option[data-current]", el.expires).textContent = keepLabel(item.expires_at);
  if (el.password) el.password.value = "";
  if (el.clear_password) el.clear_password.checked = false;
  $("[data-edit-clearpw]", dlg).hidden = !item.password;
  $("[data-tags]", dlg)?.pbSet(item.tags);
}

if (globalThis.document) wire();

function wire() {
// A drawer closed after a partial save still tells the page.
document.addEventListener("close", (e) => {
  if (!(e.target instanceof HTMLDialogElement) || e.target.id !== "item-edit" || !editing) return;
  const { item, onSaved, changed } = editing;
  editing = null;
  if (changed) onSaved?.(item, { partial: true });
}, true);

document.addEventListener("submit", async (e) => {
  const form = e.target instanceof Element ? e.target.closest("[data-edit-form]") : null;
  if (!form || e.submitter?.value === "cancel" || !editing) return;
  e.preventDefault();
  if (editing.saving) return;
  const dlg = form.closest("dialog");
  const el = form.elements;
  const state = editing;
  const { item } = state;
  const submit = e.submitter || $("button[type=submit]", form);
  submit.dataset.busyLabel = "Saving…";
  const patch = {};
  if (el.title.value.trim() !== item.title) patch.title = el.title.value;
  if (el.slug.value !== item.slug) patch.slug = el.slug.value;
  if (el.expires.value) patch.expires = el.expires.value;
  if (el.visibility.value !== item.visibility) patch.visibility = el.visibility.value;
  if (el.clear_password?.checked) patch.password = null;
  else if (el.password.value) patch.password = el.password.value;
  const tags = splitTags(el.tags.value);
  const tagsChanged = tags.join("\n") !== item.tags.join("\n");

  state.saving = true;
  pbBusy(true, submit);
  hideError(dlg);
  try {
    let patched = false;
    if (Object.keys(patch).length) {
      const res = await api("PATCH", `/api/v1/items/${item.id}`, patch);
      if (!res.ok) {
        showError(dlg, res.error);
        return;
      }
      // What the server kept is the drawer's new starting point.
      const d = res.data || {};
      item.title = d.title ?? item.title;
      item.slug = d.slug ?? item.slug;
      item.visibility = d.visibility ?? item.visibility;
      if ("expires_at" in d) item.expires_at = d.expires_at;
      if ("password" in patch) item.password = patch.password !== null;
      state.changed = true;
      patched = true;
    }
    if (tagsChanged) {
      const res = await api("PUT", `/api/v1/items/${item.id}/tags`, { tags });
      if (!res.ok) {
        if (patched) {
          fill(dlg, form, item);
          $("[data-tags]", dlg)?.pbSet(tags);
          showError(dlg, `The changes to the details were saved, but the tags were not: ${res.error} Fix the tags and save again.`);
        } else {
          showError(dlg, res.error);
        }
        return;
      }
      state.changed = true;
      item.tags = tags;
    }
    state.changed = false;
    editing = null;
    dlg.close();
    state.onSaved?.(item);
  } finally {
    state.saving = false;
    pbBusy(false, submit);
  }
});
}
