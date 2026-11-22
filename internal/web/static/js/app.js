// Behaviour for the Patchbay partials. Loaded on every page as an ES module.
// No inline handlers exist in templates; everything is wired here by attribute.

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

// Copy: button[data-copy] copies its sibling input, or data-copy-value / data-copy-target.
on(document, "[data-copy]", "click", async (_e, b) => {
  let text = b.dataset.copyValue;
  if (!text && b.dataset.copyTarget) text = document.getElementById(b.dataset.copyTarget)?.value ?? document.getElementById(b.dataset.copyTarget)?.textContent;
  if (!text) text = b.parentElement.querySelector("input,textarea")?.value;
  if (!text) return;
  try {
    await navigator.clipboard.writeText(text);
    flash(b, "Copied");
  } catch {
    flash(b, "Copy failed");
  }
});

function flash(b, label) {
  const prev = b.getAttribute("aria-label");
  const icon = b.querySelector(".pb-icon");
  const text = [...b.childNodes].find((n) => n.nodeType === Node.TEXT_NODE && n.textContent.trim());
  if (icon) icon.className = "pb-icon pb-icon--check";
  if (text) { b.dataset.prevText = text.textContent; text.textContent = label; }
  b.setAttribute("aria-label", label);
  clearTimeout(b._flash);
  b._flash = setTimeout(() => {
    if (icon) icon.className = "pb-icon pb-icon--copy";
    if (text) text.textContent = b.dataset.prevText;
    if (prev) b.setAttribute("aria-label", prev);
  }, 1500);
}

// Menus: position [popover].pb-menu under its trigger when opened.
document.addEventListener("toggle", (e) => {
  const m = e.target;
  if (!(m instanceof HTMLElement) || !m.matches(".pb-menu[popover]") || e.newState !== "open") return;
  const trigger = document.querySelector(`[popovertarget="${CSS.escape(m.id)}"]`);
  if (!trigger) return;
  const r = trigger.getBoundingClientRect();
  m.style.setProperty("position", "fixed");
  m.style.setProperty("margin", "0");
  m.style.setProperty("top", `${r.bottom + 6}px`);
  const width = m.offsetWidth;
  const left = Math.min(Math.max(8, r.right - width), window.innerWidth - width - 8);
  m.style.setProperty("left", `${left}px`);
}, true);

// Dialogs: [data-dialog-open=id] opens a <dialog>.
on(document, "[data-dialog-open]", "click", (_e, b) => {
  document.getElementById(b.dataset.dialogOpen)?.showModal();
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
  if (t.dataset.tabHash !== undefined) history.replaceState(null, "", "#" + t.getAttribute("aria-controls"));
}

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

// Progress bars: .pb-progress[data-value] -> --value (CSP forbids style attributes).
export function syncProgress(root = document) {
  root.querySelectorAll(".pb-progress[data-value]").forEach((p) => {
    p.style.setProperty("--value", `${Number(p.dataset.value) || 0}%`);
  });
}
syncProgress();
document.addEventListener("htmx:afterSwap", (e) => syncProgress(e.target));
