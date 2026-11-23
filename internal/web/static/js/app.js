// Behaviour for the Patchbay partials. Loaded on every page as an ES module.
// No inline handlers exist in templates; everything is wired here by attribute.

import { init as initControls, toast, confirm } from "./patchbay.js";

// DatePicker, TagInput, Combobox and ColorPicker: on load and in htmx swaps.
initControls();
document.addEventListener("htmx:afterSwap", (e) => initControls(e.target));

// Flash messages from the server ({{template "pb.flash"}}) show as toasts.
document.querySelectorAll("[data-toast]").forEach((el) => {
  toast({ status: el.dataset.toast, title: el.dataset.title, body: el.dataset.body });
  el.remove();
});

const on = (root, sel, type, fn) =>
  root.addEventListener(type, (e) => {
    const el = e.target.closest(sel);
    if (el && root.contains(el)) fn(e, el);
  });

// Password show/hide: button[data-reveal] inside .pb-input.
on(document, "[data-reveal]", "click", (_e, b) => {
  const input = b.parentElement.querySelector("input");
  const show = input.type === "password";
  input.type = show ? "text" : "password";
  b.firstElementChild.className = "pb-icon pb-icon--" + (show ? "eye-off" : "eye");
  b.setAttribute("aria-label", show ? "Hide password" : "Show password");
});

// Copy: button[data-copy] copies its sibling input; data-copy-value / data-copy-target
// carry the text themselves, so a button with only data-copy-value works too (item.share).
on(document, "[data-copy],[data-copy-value]", "click", async (_e, b) => {
  let text = b.dataset.copyValue;
  if (!text && b.dataset.copyTarget) text = document.getElementById(b.dataset.copyTarget)?.value ?? document.getElementById(b.dataset.copyTarget)?.textContent;
  if (!text) text = b.parentElement.querySelector("input,textarea")?.value;
  if (!text) return;
  try {
    await navigator.clipboard.writeText(text);
    flash(b, "Copied", true);
    // Icon-only buttons have no label to change, so say it in a toast.
    if (!b.textContent.trim()) toast({ ink: true, status: "ok", title: "Copied", timeout: 2000 });
  } catch {
    // The browser refused: select the value so the user can copy it by hand.
    const input = b.parentElement.querySelector("input");
    if (input) { input.type = "text"; input.focus(); input.select(); }
    flash(b, "Press ⌘C", false);
  }
});

// flash shows the copy result for 1.6 s (CopyField: check icon, "Copied", ok color).
function flash(b, label, ok) {
  const prev = b.getAttribute("aria-label");
  const icon = b.querySelector(".pb-icon");
  const text = b.querySelector("span:not(.pb-icon)") ?? [...b.childNodes].find((n) => n.nodeType === Node.TEXT_NODE && n.textContent.trim());
  if (icon && ok) icon.className = "pb-icon pb-icon--check";
  if (text && b.dataset.prevText === undefined) b.dataset.prevText = text.textContent;
  if (text) text.textContent = label;
  b.toggleAttribute("data-done", ok);
  b.setAttribute("aria-label", label);
  clearTimeout(b._flash);
  b._flash = setTimeout(() => {
    if (icon) icon.className = "pb-icon pb-icon--copy";
    if (text) text.textContent = b.dataset.prevText;
    delete b.dataset.prevText;
    b.removeAttribute("data-done");
    if (prev) b.setAttribute("aria-label", prev);
  }, 1600);
}

// Password strength: .pb-strength[data-strength] after a new-password input.
// Length counts most; mixing letters, digits and symbols adds a step.
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

// Fieldset footers: form[data-foot] shows "No changes" / "Unsaved changes" in
// .pb-foot__status and enables its footer buttons only while there are
// changes. A form re-shown with errors starts as changed.
document.querySelectorAll("form[data-foot]").forEach((form) => {
  const status = form.querySelector(".pb-foot__status");
  const buttons = form.querySelectorAll(".pb-fieldset__foot button");
  const label = status?.querySelector("[data-foot-label]");
  const set = (dirty) => {
    buttons.forEach((b) => { b.disabled = !dirty; });
    if (!status || (!dirty && status.dataset.state === "saved")) return;
    status.dataset.state = dirty ? "dirty" : "";
    status.querySelector(".pb-icon")?.remove();
    if (label) label.textContent = dirty ? "Unsaved changes" : "No changes";
  };
  const dirty = () => { if (status) delete status.dataset.state; set(true); };
  form.addEventListener("input", dirty);
  form.addEventListener("change", dirty);
  form.addEventListener("reset", () => setTimeout(() => { if (status) status.dataset.state = ""; set(false); }));
  set(!!form.querySelector("[data-invalid]") || status?.dataset.state === "error");
});

// Menus and popovers: position [popover].pb-menu / .pb-popover under its
// trigger when opened, aligned to the trigger's end (data-align="start"
// for its start).
document.addEventListener("toggle", (e) => {
  const m = e.target;
  if (!(m instanceof HTMLElement) || !m.matches(".pb-menu[popover], .pb-popover[popover]") || e.newState !== "open") return;
  const trigger = document.querySelector(`[popovertarget="${CSS.escape(m.id)}"]`);
  if (!trigger) return;
  const r = trigger.getBoundingClientRect();
  m.style.setProperty("position", "fixed");
  m.style.setProperty("margin", "0");
  m.style.setProperty("top", `${r.bottom + 6}px`);
  const width = m.offsetWidth;
  const want = m.dataset.align === "start" ? r.left : r.right - width;
  const left = Math.min(Math.max(8, want), window.innerWidth - width - 8);
  m.style.setProperty("left", `${left}px`);
}, true);

// Dialogs: [data-dialog-open=id] opens a <dialog>; its [data-dialog-reset]
// parts are emptied first (htmx fills them for the clicked row).
on(document, "[data-dialog-open]", "click", (_e, b) => {
  const d = document.getElementById(b.dataset.dialogOpen);
  d?.querySelectorAll("[data-dialog-reset]").forEach((x) => x.replaceChildren());
  d?.showModal();
});

// Tabs: .pb-tabs > .pb-tab[aria-controls].
on(document, ".pb-tabs .pb-tab", "click", (_e, t) => selectTab(t));
on(document, ".pb-tabs", "keydown", (e, list) => {
  if (e.key !== "ArrowRight" && e.key !== "ArrowLeft") return;
  const tabs = [...list.querySelectorAll(".pb-tab")];
  const i = tabs.indexOf(document.activeElement);
  if (i < 0) return;
  const next = tabs[(i + (e.key === "ArrowRight" ? 1 : tabs.length - 1)) % tabs.length];
  next.focus();
  selectTab(next);
});
function selectTab(t) {
  const tabs = t.closest(".pb-tabs").querySelectorAll(".pb-tab");
  tabs.forEach((x) => {
    const sel = x === t;
    x.setAttribute("aria-selected", String(sel));
    x.tabIndex = sel ? 0 : -1;
    const panel = document.getElementById(x.getAttribute("aria-controls"));
    if (panel) panel.hidden = !sel;
  });
  // A tab with data-hides="<selector>" hides those elements while it is selected
  // (e.g. options that only apply to the other tabs).
  tabs.forEach((x) => {
    if (x.dataset.hides) document.querySelectorAll(x.dataset.hides).forEach((el) => { el.hidden = x === t; });
  });
  if (t.dataset.tabHash !== undefined) history.replaceState(null, "", "#" + t.getAttribute("aria-controls"));
}
// Opens the tab whose panel id is in the URL fragment (e.g. /#panel-secret),
// on load and when a link changes the fragment of the open page.
function tabFromHash() {
  if (location.hash.length < 2) return;
  const t = document.querySelector(`.pb-tabs .pb-tab[data-tab-hash][aria-controls="${CSS.escape(location.hash.slice(1))}"]`);
  if (t) selectTab(t);
}
tabFromHash();
window.addEventListener("hashchange", tabFromHash);

// Number steppers: [data-inc] / [data-dec] inside .pb-input.
on(document, "[data-inc],[data-dec]", "click", (_e, b) => {
  const i = b.parentElement.querySelector("input");
  b.hasAttribute("data-inc") ? i.stepUp() : i.stepDown();
  i.dispatchEvent(new Event("input", { bubbles: true }));
});

// Dismissable notices.
on(document, "[data-dismiss]", "click", (_e, b) => b.closest(".pb-notice")?.remove());

// Filter pills toggle aria-pressed unless they are links.
on(document, "button.pb-pill[aria-pressed]", "click", (_e, b) => {
  if (b.dataset.manual === undefined) b.setAttribute("aria-pressed", String(b.getAttribute("aria-pressed") !== "true"));
});

// Shared confirm ({{template "confirm" ...}}): a submit button with
// data-confirm-title asks first, then submits its form as itself (so its
// name/value and formaction still apply). data-confirm-body, -label
// (the action, e.g. "Revoke key") and -text (type this to confirm) are
// optional; danger buttons and menu items get the danger style.
on(document, "button[data-confirm-title]", "click", async (e, b) => {
  e.preventDefault();
  const ok = await confirm({
    title: b.dataset.confirmTitle,
    body: b.dataset.confirmBody,
    confirm: b.dataset.confirmLabel,
    typeToConfirm: b.dataset.confirmText,
    danger: b.hasAttribute("data-confirm-danger") || b.matches(".pb-btn--danger, .pb-menu__item--danger"),
  });
  if (ok && b.form) b.form.requestSubmit(b);
});

// ---- Shell behaviours. Stand-ins until the kit's patchbay.mjs (pbShell,
// pbThemeSwitch) replaces them.

// Theme switches ([data-theme-switch], pb.theme): show the saved choice,
// apply and keep a new one; "system" follows the OS setting live.
const themeMedia = matchMedia("(prefers-color-scheme: dark)");
function applyTheme(choice) {
  const root = document.documentElement;
  const dark = choice === "dark" || (choice === "system" && themeMedia.matches);
  root.dataset.theme = dark ? "dark" : "light";
  root.dataset.themeChoice = choice;
}
function syncThemeSwitches() {
  const choice = document.documentElement.dataset.themeChoice || "system";
  document.querySelectorAll("[data-theme-switch] input").forEach((i) => { i.checked = i.value === choice; });
}
syncThemeSwitches();
on(document, "[data-theme-switch] input", "change", (_e, i) => {
  if (!i.checked) return;
  applyTheme(i.value);
  try { localStorage.setItem("pb-theme", i.value); } catch { /* storage blocked: this page only */ }
  syncThemeSwitches();
});
themeMedia.addEventListener("change", () => {
  if ((document.documentElement.dataset.themeChoice || "system") === "system") applyTheme("system");
});

// AppShell drawer (under 900px): the menu button opens the nav; Escape,
// a click outside or following a link closes it.
function setDrawer(shell, open) {
  shell.toggleAttribute("data-nav-open", open);
  shell.querySelector("[data-nav-toggle]")?.setAttribute("aria-expanded", String(open));
}
on(document, "[data-nav-toggle]", "click", (_e, b) => {
  const shell = b.closest("[data-shell]");
  if (shell) setDrawer(shell, !shell.hasAttribute("data-nav-open"));
});
document.addEventListener("click", (e) => {
  const shell = document.querySelector("[data-shell][data-nav-open]");
  if (!shell) return;
  const nav = shell.querySelector(".pb-shell__nav");
  if (e.target.closest("[data-nav-toggle]")) return;
  if (!nav?.contains(e.target) || e.target.closest("a[href]")) setDrawer(shell, false);
});
document.addEventListener("keydown", (e) => {
  const shell = document.querySelector("[data-shell][data-nav-open]");
  if (e.key !== "Escape" || !shell) return;
  setDrawer(shell, false);
  shell.querySelector("[data-nav-toggle]")?.focus();
});

// Nav groups: button.pb-nav__item[aria-expanded] shows or hides the
// .pb-nav__sub after it.
on(document, "button.pb-nav__item[aria-expanded]", "click", (_e, b) => {
  const open = b.getAttribute("aria-expanded") !== "true";
  b.setAttribute("aria-expanded", String(open));
  const sub = document.getElementById(b.getAttribute("aria-controls") ?? "") ?? b.nextElementSibling;
  if (sub) sub.hidden = !open;
});

// Progress bars: .pb-progress[data-value] -> --value (CSP forbids style attributes).
export function syncProgress(root = document) {
  root.querySelectorAll(".pb-progress[data-value]").forEach((p) => {
    p.style.setProperty("--value", `${Number(p.dataset.value) || 0}%`);
  });
}
syncProgress();
document.addEventListener("htmx:afterSwap", (e) => syncProgress(e.target));

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
