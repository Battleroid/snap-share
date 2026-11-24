// The item page: Edit opens the shared drawer (js/item-edit.js) with the
// item's JSON from the button, and a save reloads the page.

import { openEdit } from "./item-edit.js";

document.addEventListener("click", (e) => {
  const b = e.target instanceof Element ? e.target.closest("[data-item-edit]") : null;
  if (!b) return;
  openEdit(JSON.parse(b.dataset.item), () => location.reload());
});
