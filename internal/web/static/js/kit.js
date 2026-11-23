// The kit's behaviours (vendor/patchbay/patchbay.mjs) for every page, bound
// with the hooks snap leaves out, plus snap's own TagInput. app.js runs
// init(document) on load and init(el) on htmx:load; scripts that add markup
// (composer.js) call init(el) too. pbInit binds each element once, so a
// second call on the same markup does nothing.

import pbInit from "../vendor/patchbay/patchbay.mjs";
import { tags } from "./tags.js";

// Hooks snap does not take from the kit:
//   strength   snap's own scoring and words (app.js)
//   table      tables sort and select on the server (dashboard.js, admin)
//   bulk       the dashboard keeps its own bulk bar (dashboard.js)
//   inbox      inbox.js wraps the bell and the page with snap's options
//   tags       the kit's TagInput keeps no form value; tags.js does
//   codearea   composer.js keeps the editable CodeArea in step itself
export const SKIP = ["strength", "table", "bulk", "inbox", "tags", "codearea"];

const tagBoxes = new WeakSet();

// init binds the kit hooks and snap's TagInput under root.
export function init(root = document) {
  pbInit(root, { skip: SKIP });
  const boxes = [...root.querySelectorAll("[data-tags]")];
  if (root instanceof Element && root.matches("[data-tags]")) boxes.unshift(root);
  for (const box of boxes) {
    if (tagBoxes.has(box) || box.closest("[data-pb-skip~=tags]")) continue;
    tagBoxes.add(box);
    tags(box);
  }
}
