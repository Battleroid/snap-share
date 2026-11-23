// Behaviour for every page. Loaded as an ES module. The kit's behaviours
// (vendor/patchbay/patchbay.mjs: pickers, combobox, copy, reveal, tabs,
// popovers and menus, form sections, the shell drawer, theme switches,
// data-vars, progress, QR codes, relative times, …) are bound by kit.js;
// what is left here is snap's own. No inline handlers exist in templates;
// everything is wired by attribute.

import { pbToast, pbConfirm } from "../vendor/patchbay/patchbay.mjs";
import { init } from "./kit.js";

// The kit hooks: on load, and on whatever htmx adds (each element binds once).
init(document);
document.addEventListener("htmx:load", (e) => init(e.detail?.elt ?? e.target));

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

// Password strength: .pb-strength[data-strength] after a new-password input
// (snap's scoring; the kit's strength hook is skipped). Length counts most;
// mixing letters, digits and symbols adds a step.
on(document, "input[type=password], input[type=text]", "input", (_e, input) => {
  const meter = input.closest(".pb-field")?.querySelector("[data-strength]");
  if (!meter) return;
  const v = input.value;
  const kinds = [/[a-z]/, /[A-Z]/, /\d/, /[^A-Za-z0-9]/].filter((r) => r.test(v)).length;
  let level = 0;
  if (v) level = v.length < 10 ? 1 : 2 + (v.length >= 14 || kinds >= 3 ? 1 : 0) + (v.length >= 20 || (v.length >= 14 && kinds >= 3) ? 1 : 0);
  meter.dataset.level = String(level);
  meter.lastElementChild.textContent = ["—", "too short", "fair", "good", "strong"][level];
});

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

// Tabs: the kit selects them (roving tabindex; arrow, Home and End keys;
// pb:tabchange); snap adds data-hides="<selector>" (elements hidden while
// that tab is selected, e.g. options that only apply to the others) and
// data-tab-hash (the selected tab's panel id in the URL fragment).
document.addEventListener("pb:tabchange", (e) => {
  const t = e.detail?.tab;
  if (!t) return;
  t.closest(".pb-tabs").querySelectorAll(".pb-tab").forEach((x) => {
    if (x.dataset.hides) document.querySelectorAll(x.dataset.hides).forEach((el) => { el.hidden = x === t; });
  });
  if (t.dataset.tabHash !== undefined) history.replaceState(null, "", "#" + t.getAttribute("aria-controls"));
});
// Opens the tab whose panel id is in the URL fragment (e.g. /#panel-secret),
// on load and when a link changes the fragment of the open page.
function tabFromHash() {
  if (location.hash.length < 2) return;
  document.querySelector(`.pb-tabs .pb-tab[data-tab-hash][aria-controls="${CSS.escape(location.hash.slice(1))}"]`)?.click();
}
tabFromHash();
window.addEventListener("hashchange", tabFromHash);

// Filter pills toggle aria-pressed unless they are links.
on(document, "button.pb-pill[aria-pressed]", "click", (_e, b) => {
  if (b.dataset.manual === undefined) b.setAttribute("aria-pressed", String(b.getAttribute("aria-pressed") !== "true"));
});

// Shared confirm ({{template "confirm" ...}}): a submit button with
// data-confirm-title asks first (the kit's pbConfirm, which shows the body
// as text), then submits its form as itself (so its name/value and
// formaction still apply). data-confirm-body, -label (the action, e.g.
// "Revoke key") and -text (type this to confirm) are optional; danger
// buttons and menu items get the danger style.
on(document, "button[data-confirm-title]", "click", async (e, b) => {
  e.preventDefault();
  const ok = await pbConfirm({
    title: b.dataset.confirmTitle,
    body: b.dataset.confirmBody,
    confirm: b.dataset.confirmLabel,
    typeToConfirm: b.dataset.confirmText,
    danger: b.hasAttribute("data-confirm-danger") || b.matches(".pb-btn--danger, .pb-menu__item--danger"),
  });
  if (!ok || !b.form) return;
  // The partial puts the button's name and value in data-confirm-name/-value,
  // so only a confirmed submit carries them.
  if (b.dataset.confirmName) {
    b.form.querySelector("input[data-confirm-field]")?.remove();
    const f = document.createElement("input");
    f.type = "hidden";
    f.name = b.dataset.confirmName;
    f.value = b.dataset.confirmValue ?? "";
    f.dataset.confirmField = "";
    b.form.append(f);
  }
  b.form.requestSubmit(b);
});

// Confirm buttons come disabled (nothing irreversible is sent unconfirmed
// without scripts); enable them now that the handler above is wired, on
// load and in whatever htmx adds.
function enableConfirms(root) {
  const list = [...root.querySelectorAll("button[data-confirm-enable]")];
  if (root instanceof Element && root.matches("button[data-confirm-enable]")) list.push(root);
  for (const b of list) {
    b.disabled = false;
    b.removeAttribute("data-confirm-enable");
  }
}
enableConfirms(document);
document.addEventListener("htmx:load", (e) => enableConfirms(e.detail?.elt ?? e.target));

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
