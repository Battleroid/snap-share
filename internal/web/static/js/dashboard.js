// Dashboard: selection, bulk actions, row menus and the edit dialog.
// htmx swaps #dash-results when the filters change (and the whole body on a
// history restore), so elements are looked up when needed and every listener
// is delegated from document.

import { pbToast as toast } from "../vendor/patchbay/patchbay.mjs";

const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];
const on = (type, sel, fn) =>
  document.addEventListener(type, (e) => {
    const el = e.target instanceof Element ? e.target.closest(sel) : null;
    if (el) fn(e, el);
  });

// Selection: the checked rows on this page, or (all) every item matching the
// filter, sent as the filter plus the count the page showed. A row menu acts
// on its own item (single).
let all = false;
let single = null;

const results = () => document.getElementById("dash-results");
const meta = () => $("[data-results]", results() ?? document)?.dataset ?? {};
const checks = () => $$("[data-check]", results() ?? document);
const total = () => Number(meta().total || 0);
const picked = () => checks().filter((c) => c.checked);
const noun = (n) => (n === 1 ? "1 item" : `${n} items`);

function count() {
  if (single) return 1;
  return all ? total() : picked().length;
}

function selection() {
  if (single) return { ids: [single.id] };
  if (all) {
    const m = meta();
    return { filter: m.query || "", expected: Number(m.total), created_before: m.bound };
  }
  return { ids: picked().map((c) => Number(c.value)) };
}

function sync() {
  const bar = $("[data-bulkbar]");
  if (!bar) return;
  const boxes = checks();
  const n = boxes.filter((c) => c.checked).length;
  if (all && n !== boxes.length) all = false;
  boxes.forEach((c) => c.closest("[data-row]")?.setAttribute("aria-selected", String(c.checked)));
  const head = $("[data-check-all]", results() ?? document);
  if (head) {
    head.checked = boxes.length > 0 && n === boxes.length;
    head.indeterminate = n > 0 && n < boxes.length;
  }
  const c = all ? total() : n;
  bar.hidden = c === 0;
  $("[data-selcount]", bar).textContent = all ? `all ${c} selected` : c ? `${c} selected` : "none selected";
  const more = $("[data-select-all]", bar);
  more.hidden = all || n === 0 || n !== boxes.length || total() <= boxes.length;
  $("[data-total]", more).textContent = String(total());
  $("[data-select-none]", bar).hidden = c === 0;
  $$("[data-bulk], [data-bulk-menu]", bar).forEach((b) => { b.disabled = c === 0; });
}

on("change", "[data-check]", () => sync());
on("change", "[data-check-all]", (_e, box) => {
  all = false;
  checks().forEach((c) => { c.checked = box.checked; });
  sync();
});
on("click", "[data-select-all]", () => {
  checks().forEach((c) => { c.checked = true; });
  all = true;
  sync();
});
on("click", "[data-select-none]", () => {
  checks().forEach((c) => { c.checked = false; });
  all = false;
  sync();
});

// The search box asks for results as you type; its change event (on blur)
// must not resubmit the form with the same values.
document.addEventListener("change", (e) => {
  if (e.target instanceof Element && e.target.matches('#dash-form input[type="search"]')) e.stopPropagation();
}, true);

// Type pills set the form's hidden type and resubmit it (htmx handles the submit).
on("click", "[data-type]", (_e, b) => {
  const form = document.getElementById("dash-form");
  $("[data-type-input]", form).value = b.dataset.type;
  form.requestSubmit();
});

// The heading and its meta line sit outside the swapped results, so they
// follow the results' own data-title and data-meta (Items / Albums, the count).
function syncHead() {
  const m = meta();
  const h1 = $(".pb-pagehead__title");
  if (h1 && m.title && h1.textContent !== m.title) h1.textContent = m.title;
  const desc = $(".pb-pagehead__desc .pb-mono");
  if (desc && m.meta !== undefined) desc.textContent = m.meta;
}

document.addEventListener("htmx:afterSettle", () => {
  all = false;
  sync();
  syncHead();
});
sync();

// API calls.

async function api(method, url, body) {
  let res;
  try {
    res = await fetch(url, {
      method,
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", Accept: "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    return { ok: false, error: "The server could not be reached." };
  }
  let data = null;
  try { data = await res.json(); } catch { /* 204 */ }
  return { ok: res.ok, status: res.status, data, error: data?.error || `Request failed (${res.status}).` };
}

function refresh() {
  if (window.htmx && results()) {
    window.htmx.ajax("GET", location.pathname + location.search, { target: "#dash-results", swap: "innerHTML" });
  } else {
    location.reload();
  }
}

// notify reports a finished action as a toast; link adds an Open action.
function notify(title, link) {
  const actions = link ? [{ label: link.text, onClick: () => { location.href = link.href; } }] : [];
  toast({ status: "ok", title, actions });
}

function showError(dlg, msg) {
  const box = $("[data-error]", dlg);
  if (!box) return;
  $(".pb-notice__title", box).textContent = msg;
  box.hidden = false;
}

function hideError(dlg) {
  const box = $("[data-error]", dlg);
  if (box) box.hidden = true;
}

const split = (s) => s.split(/[\n,;]+/).map((x) => x.trim()).filter(Boolean);

function closeMenus() {
  $$(".pb-menu[popover]").forEach((m) => { try { m.hidePopover(); } catch { /* closed */ } });
}

// Bulk dialogs.

function openBulk(id) {
  const dlg = document.getElementById(id);
  if (!dlg) return;
  const n = count();
  const form = $("form", dlg);
  form.reset();
  $("[data-tags]", dlg)?.pbSet([]);
  hideError(dlg);
  $$("[data-count]", dlg).forEach((e) => { e.textContent = noun(n); });
  const name = single ? single.name : noun(n);
  if (id === "dash-delete") $(".pb-dialog__title", dlg).textContent = `Delete ${name}?`;
  if (id === "dash-email") {
    $("[data-subject]", dlg).textContent = name;
    $("[data-count-noun]", dlg).textContent = n === 1 ? "link" : "links";
  }
  if (id === "dash-album") syncAlbum(form);
  closeMenus();
  dlg.showModal();
}

function syncAlbum(form) {
  const sel = $("[data-album-select]", form);
  $("[data-album-new]", form).hidden = sel.value !== "new";
}
on("change", "[data-album-select]", (_e, sel) => syncAlbum(sel.form));

on("click", "[data-bulk-open]", (_e, b) => {
  single = null;
  openBulk(b.dataset.bulkOpen);
});

const done = {
  set_expiry: (d) => `Expiry set on ${noun(d.count)}.`,
  set_visibility: (d) => `Visibility set on ${noun(d.count)}.`,
  set_password: (d) => `Password set on ${noun(d.count)}.`,
  clear_password: (d) => `Password removed from ${noun(d.count)}.`,
  add_to_album: (d) => `Added ${noun(d.count)} to the album.`,
  new_album: (d) => `Created ${d.album.name} with ${noun(d.count)}.`,
  add_tags: (d) => `Tagged ${noun(d.count)}.`,
  remove_tags: (d) => `Removed tags from ${noun(d.count)}.`,
  delete: (d) => `Deleted ${noun(d.count)}.`,
  send_email: (d) => `Sent to ${d.sent} ${d.sent === 1 ? "address" : "addresses"}.`,
};

on("submit", "[data-bulk-form]", async (e, form) => {
  if (e.submitter?.value === "cancel") return; // method=dialog closes it
  e.preventDefault();
  const dlg = form.closest("dialog");
  const el = form.elements;
  let action = form.dataset.bulkForm;
  const params = {};
  switch (action) {
    case "set_expiry": params.expires = el.expires.value; break;
    case "set_visibility": params.visibility = el.visibility.value; break;
    case "set_password": params.password = el.password.value; break;
    case "album":
      if (el.album_id.value === "new") {
        action = "new_album";
        params.title = el.title.value;
      } else {
        action = "add_to_album";
        params.album_id = Number(el.album_id.value);
      }
      break;
    case "tags":
      action = el.mode.value;
      params.tags = split(el.tags.value);
      break;
    case "send_email": params.to = split(el.to.value); break;
  }
  const submit = e.submitter;
  if (submit) submit.disabled = true;
  const res = await api("POST", "/api/v1/items/bulk", { ...selection(), action, params });
  if (submit) submit.disabled = false;
  if (!res.ok) {
    showError(dlg, res.error);
    return;
  }
  dlg.close();
  single = null;
  const link = action === "new_album" ? { href: res.data.album.url, text: "Open the album" } : null;
  notify(done[action]?.(res.data) ?? "Done.", link);
  if (action === "new_album") addAlbumOption(res.data.album);
  refresh();
});

function addAlbumOption(album) {
  const sel = $("[data-album-select]");
  if (!sel) return;
  const o = document.createElement("option");
  o.value = String(album.id);
  o.textContent = album.name;
  sel.prepend(o);
}

// Zip: the server checks the selection and answers with a short-lived URL,
// which downloads as an attachment.
on("click", "[data-bulk=download_zip]", async (_e, b) => {
  b.disabled = true;
  const res = await api("POST", "/api/v1/items/bulk", { ...selection(), action: "download_zip" });
  b.disabled = false;
  if (!res.ok) {
    notifyError(res.error);
    return;
  }
  notify(`Downloading ${res.data.count === 1 ? "1 file" : `${res.data.count} files`}.`);
  location.assign(res.data.url);
});

// notifyError shows a problem above the table, where it stays until dismissed.
function notifyError(msg) {
  const box = $("[data-dash-notice]");
  if (!box) return;
  const n = document.createElement("div");
  n.className = "pb-notice";
  n.dataset.status = "down";
  n.setAttribute("role", "alert");
  const icon = document.createElement("span");
  icon.className = "pb-icon pb-icon--status-down";
  icon.setAttribute("aria-hidden", "true");
  const t = document.createElement("p");
  t.className = "pb-notice__title";
  t.textContent = msg;
  const x = document.createElement("button");
  x.type = "button";
  x.className = "pb-btn pb-btn--ghost pb-btn--icon pb-btn--sm";
  x.setAttribute("aria-label", "Dismiss");
  x.dataset.dismiss = "";
  const xi = document.createElement("span");
  xi.className = "pb-icon pb-icon--close";
  xi.setAttribute("aria-hidden", "true");
  x.append(xi);
  n.append(icon, t, x);
  box.replaceChildren(n);
}

// Row menus.

on("click", "[data-row-action]", (_e, b) => {
  const row = b.closest("[data-row]");
  if (!row) return;
  const item = JSON.parse(row.dataset.item);
  closeMenus();
  switch (b.dataset.rowAction) {
    case "edit": openEdit(item); break;
    case "album":
      single = { id: item.id, name: item.name };
      openBulk("dash-album");
      break;
    case "delete":
      single = { id: item.id, name: item.name };
      openBulk("dash-delete");
      break;
  }
});

document.addEventListener("close", (e) => {
  if (e.target instanceof HTMLDialogElement && e.target.closest("[data-dashboard]")) single = null;
}, true);

// Edit dialog: PATCH /api/v1/items/{id} with what changed, then the tags.

let editing = null;

function openEdit(item) {
  const dlg = document.getElementById("dash-edit");
  const form = $("form", dlg);
  form.reset();
  hideError(dlg);
  editing = item;
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

on("submit", "[data-edit-form]", async (e, form) => {
  if (e.submitter?.value === "cancel") return;
  e.preventDefault();
  const dlg = form.closest("dialog");
  const el = form.elements;
  const item = editing;
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
  const tags = split(el.tags.value);
  if (tags.join("\n") !== item.tags.join("\n")) {
    const res = await api("PUT", `/api/v1/items/${item.id}/tags`, { tags });
    if (!res.ok) {
      showError(dlg, res.error);
      return;
    }
  }
  dlg.close();
  notify(`Saved ${item.name}.`);
  refresh();
});
