// The item page: Edit opens the shared drawer (js/item-edit.js) with the
// item's JSON from the button, and a save reloads the page. The reload would
// lose the toast, so a save leaves a note in sessionStorage that the next
// load turns into the "Saved" toast, as the dashboard's saves do.

import { pbToast } from "../vendor/patchbay/patchbay.mjs";
import { openEdit } from "./item-edit.js";

const KEY = "snap-item-saved";

try {
  const name = sessionStorage.getItem(KEY);
  if (name !== null) {
    sessionStorage.removeItem(KEY);
    pbToast({ status: "ok", title: name ? `Saved ${name}.` : "Saved." });
  }
} catch {
  // storage unavailable: the page just updates
}

document.addEventListener("click", (e) => {
  const b = e.target instanceof Element ? e.target.closest("[data-item-edit]") : null;
  if (!b) return;
  const item = JSON.parse(b.dataset.item);
  openEdit(item, (_saved, state) => {
    if (!state?.partial) {
      try {
        sessionStorage.setItem(KEY, item.name || "");
      } catch {
        // storage unavailable
      }
    }
    location.reload();
  });
});
