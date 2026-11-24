// The Edit drawer for one item (_partials/item-edit.html), used by the
// dashboard and the item page. openEdit fills it from the item's JSON
// (id name title slug kind visibility password expires_at tags); Save
// PATCHes /api/v1/items/{id} with only what changed, then PUTs the tags if
// they changed, and calls onSaved once both are done. Errors stay in the
// drawer.

import { api } from "./api.js";

const $ = (sel, root = document) => root.querySelector(sel);

export const splitTags = (s) => s.split(/[\n,;]+/).map((x) => x.trim()).filter(Boolean);

let editing = null; // { item, onSaved }

function showError(dlg, msg) {
  const box = $("[data-error]", dlg);
  if (!box) return;
  $(".pb-notice__title", box).textContent = msg;
  box.hidden = false;
}

export function openEdit(item, onSaved) {
  const dlg = document.getElementById("item-edit");
  if (!dlg) return;
  const form = $("form", dlg);
  form.reset();
  const err = $("[data-error]", dlg);
  if (err) err.hidden = true;
  editing = { item, onSaved };
  const el = form.elements;
  $(".pb-dialog__title", dlg).textContent = `Edit ${item.name}`;
  $(".pb-dialog__sub", dlg).textContent = `${location.host}/${item.slug}`;
  el.title.value = item.title;
  el.slug.value = item.slug;
  el.visibility.value = item.visibility;
  el.expires.value = "";
  const keep = $("option[data-current]", el.expires);
  keep.textContent = item.expires_at ? `keep (${item.expires_at.slice(0, 16).replace("T", " ")})` : "keep (never)";
  $("[data-edit-clearpw]", dlg).hidden = !item.password;
  $("[data-tags]", dlg)?.pbSet(item.tags);
  dlg.showModal();
}

document.addEventListener("submit", async (e) => {
  const form = e.target instanceof Element ? e.target.closest("[data-edit-form]") : null;
  if (!form || e.submitter?.value === "cancel" || !editing) return;
  e.preventDefault();
  const dlg = form.closest("dialog");
  const el = form.elements;
  const { item, onSaved } = editing;
  const patch = {};
  if (el.title.value.trim() !== item.title) patch.title = el.title.value;
  if (el.slug.value !== item.slug) patch.slug = el.slug.value;
  if (el.expires.value) patch.expires = el.expires.value;
  if (el.visibility.value !== item.visibility) patch.visibility = el.visibility.value;
  if (el.clear_password?.checked) patch.password = null;
  else if (el.password.value) patch.password = el.password.value;
  if (Object.keys(patch).length) {
    const res = await api("PATCH", `/api/v1/items/${item.id}`, patch);
    if (!res.ok) {
      showError(dlg, res.error);
      return;
    }
  }
  const tags = splitTags(el.tags.value);
  if (tags.join("\n") !== item.tags.join("\n")) {
    const res = await api("PUT", `/api/v1/items/${item.id}/tags`, { tags });
    if (!res.ok) {
      showError(dlg, res.error);
      return;
    }
  }
  dlg.close();
  editing = null;
  onSaved?.(item);
});
