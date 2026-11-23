// Stats and secret timeline pages. The charts are server-rendered SVG; this
// only wires the controls:
//   form[data-autosubmit]   reloads with the chosen range on change
//   form[data-autosave]     posts the per-item notification override on change
//   input[data-secret-notify=<url>]  the secret's notify Switch
//   [data-viewers] buttons  filter a secret's timeline to one viewer
// Each form keeps its submit button for use without scripts; it is hidden here.
// data-vars (storage bars, meters, dashboard spans) is the kit's vars hook.

import { chosen, filterViewers } from "./viewers.js";

document.querySelectorAll("[data-autosubmit-button],[data-autosave-button]").forEach((b) => { b.hidden = true; });

document.addEventListener("click", (e) => {
  const b = e.target.closest?.("[data-viewers] button[data-viewer]");
  if (!b) return;
  const strip = b.closest("[data-viewers]");
  filterViewers(strip, chosen(b));
});

async function post(url, body) {
  const res = await fetch(url, { method: "POST", headers: { Accept: "application/json" }, body, credentials: "same-origin" });
  if (res.ok) return "";
  try { return (await res.json()).error || "Could not save."; } catch { return "Could not save."; }
}

document.addEventListener("change", async (e) => {
  const t = e.target;
  const range = t.closest("form[data-autosubmit]");
  if (range) {
    range.submit();
    return;
  }
  const save = t.closest("form[data-autosave]");
  if (save) {
    const status = save.querySelector("[data-autosave-status]");
    if (status) status.textContent = "";
    let err;
    try { err = await post(save.action, new URLSearchParams(new FormData(save))); } catch { err = "Could not save."; }
    if (status) status.textContent = err || "saved";
    return;
  }
  if (t.matches("input[data-secret-notify]")) {
    const box = document.querySelector("[data-secret-notify-error]");
    const on = t.checked;
    t.disabled = true;
    let err;
    try { err = await post(t.dataset.secretNotify, new URLSearchParams({ enabled: on ? "1" : "0" })); } catch { err = "Could not save."; }
    t.disabled = false;
    if (err) t.checked = !on;
    if (box) { box.textContent = err; box.hidden = !err; }
  }
});
