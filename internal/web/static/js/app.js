// Behaviour for every page. Loaded as an ES module. The kit's behaviours
// (vendor/patchbay/patchbay.mjs: pickers, combobox, copy, reveal, tabs,
// popovers and menus, form sections, the shell drawer, theme switches,
// data-vars, progress, QR codes, relative times, …) are bound by kit.js;
// what is left here is snap's own. No inline handlers exist in templates;
// everything is wired by attribute.

import { pbToast, pbStrength, pbBusy } from "../vendor/patchbay/patchbay.mjs";
import { init } from "./kit.js";

// The kit hooks: on load, and on whatever htmx adds (each element binds once).
init(document);
document.addEventListener("htmx:load", (e) => init(e.detail?.elt ?? e.target));

// Busy regions: while htmx replaces a region (filters, search, paging, a
// dialog's body) it is aria-busy, which the kit dims and spins after 300 ms,
// so fast swaps never flash. A boosted page load shows the top bar instead
// of dimming the page; the media tiles' 5-second polls show nothing.
const busy = new Map(); // request element → what was marked busy
document.addEventListener("htmx:beforeRequest", (e) => {
  const elt = e.detail?.elt, target = e.detail?.target;
  if (!(elt instanceof Element) || /^\s*every\b/.test(elt.getAttribute("hx-trigger") ?? "")) return;
  const region = target instanceof Element && target !== document.body ? target : null;
  pbBusy(true, region ?? undefined);
  busy.set(elt, region);
});
const settle = (e) => {
  const elt = e.detail?.elt;
  if (!busy.has(elt)) return;
  const region = busy.get(elt);
  busy.delete(elt);
  pbBusy(false, region ?? undefined);
};
document.addEventListener("htmx:afterRequest", settle);
document.addEventListener("htmx:sendError", settle);

// Flash messages from the server ({{template "pb.flash"}}) show as toasts.
document.querySelectorAll("[data-toast]").forEach((el) => {
  pbToast({ status: el.dataset.toast, title: el.dataset.title, body: el.dataset.body });
  el.remove();
});

const on = (root, sel, type, fn) =>
  root.addEventListener(type, (e) => {
    const el = e.target.closest?.(sel);
    if (el && root.contains(el)) fn(e, el);
  });

// Password strength: the kit's meter (.pb-strength in the field) with snap's
// scoring. Length counts most; mixing letters, digits and symbols adds a step.
pbStrength.score = (v) => {
  const kinds = [/[a-z]/, /[A-Z]/, /\d/, /[^A-Za-z0-9]/].filter((r) => r.test(v)).length;
  let level = 0;
  if (v) level = v.length < 10 ? 1 : 2 + (v.length >= 14 || kinds >= 3 ? 1 : 0) + (v.length >= 20 || (v.length >= 14 && kinds >= 3) ? 1 : 0);
  return { level, label: ["—", "too short", "fair", "good", "strong"][level] };
};

// Type to confirm: a dialog's danger button stays disabled until the input
// with data-confirm-text holds exactly that text (it works without script).
document.querySelectorAll("input[data-confirm-text]").forEach((input) => {
  const btn = input.form?.querySelector(".pb-btn--danger");
  if (!btn) return;
  const sync = () => { btn.disabled = input.value !== input.dataset.confirmText; };
  input.addEventListener("input", sync);
  input.form.addEventListener("reset", () => setTimeout(sync));
  sync();
});

// Dialogs: [data-dialog-open=id] opens a <dialog>; its [data-dialog-reset]
// parts are emptied first (htmx fills them for the clicked row).
on(document, "[data-dialog-open]", "click", (_e, b) => {
  const d = document.getElementById(b.dataset.dialogOpen);
  d?.querySelectorAll("[data-dialog-reset]").forEach((x) => x.replaceChildren());
  d?.showModal();
});

// Tabs are the kit's: roving tabindex and arrow keys, data-tab-hash (the
// selected panel's id in the URL fragment, restored on load and hashchange)
// and data-hides (elements hidden while that tab is selected).

// Filter pills toggle aria-pressed unless they are links.
on(document, "button.pb-pill[aria-pressed]", "click", (_e, b) => {
  if (b.dataset.manual === undefined) b.setAttribute("aria-pressed", String(b.getAttribute("aria-pressed") !== "true"));
});

// Shared confirm ({{template "confirm" ...}} and page.action's "confirm"):
// the kit's confirm hook (kit.js binds it, also in what htmx adds) asks with
// pbConfirm, then clicks the button again, so its name, value and formaction
// apply. Irreversible ones are served disabled with data-confirm-enable and
// the hook enables them once bound, so nothing is sent unconfirmed without
// scripts.

// The dashboard switches between Items and Albums (?type=album) through htmx
// without a page load; keep the sidebar's aria-current in step with the URL.
function syncDashNav() {
  if (location.pathname !== "/dashboard") return;
  const want = new URLSearchParams(location.search).get("type") === "album" ? "/dashboard?type=album" : "/dashboard";
  document.querySelectorAll('nav[aria-label="Main"] a[href="/dashboard"], nav[aria-label="Main"] a[href="/dashboard?type=album"]').forEach((a) => {
    if (a.getAttribute("href") === want) a.setAttribute("aria-current", "page");
    else a.removeAttribute("aria-current");
  });
}
document.addEventListener("htmx:pushedIntoHistory", syncDashNav);
document.addEventListener("htmx:replacedInHistory", syncDashNav);
window.addEventListener("popstate", syncDashNav);
