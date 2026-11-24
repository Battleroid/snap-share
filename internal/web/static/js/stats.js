// Stats and secret timeline pages. The chart is the kit's time-series (bound by kit.js); this
// only wires the controls:
//   form[data-autosave]     posts the per-item notification override on change
//   input[data-secret-notify=<url>]  the secret's notify Switch
//   [data-viewers] buttons  filter a secret's timeline to one viewer
// form[data-autosubmit] (the range) is the kit's hook; the autosave form keeps its button for use without scripts, hidden here.
// data-vars (storage bars, meters, dashboard spans) is the kit's vars hook.

import { chosen, filterViewers } from "./viewers.js";

document.querySelectorAll("[data-autosave-button]").forEach((b) => { b.hidden = true; });

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
