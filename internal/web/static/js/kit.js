// The kit's behaviours (vendor/patchbay/patchbay.mjs) for every page, bound
// with the hooks snap leaves out. app.js runs init(document) on load and
// init(el) on htmx:load; scripts that add markup (composer.js) call init(el)
// too. pbInit binds each element once, so a second call on the same markup
// does nothing.

import pbInit from "../vendor/patchbay/patchbay.mjs";

// Hooks snap does not take from the kit:
//   strength   snap's own scoring and words (app.js)
//   table      tables sort and select on the server (dashboard.js, admin)
//   inbox      inbox.js binds the bell and the page itself, with the
//              server's unread count (the bare hook would show the empty
//              panel's zero until then)
//   codearea   composer.js keeps the editable CodeArea in step itself
export const SKIP = ["strength", "table", "inbox", "codearea"];

// init binds the kit hooks under root.
export function init(root = document) {
  pbInit(root, { skip: SKIP });
}

// TagInput (the kit's tags hook keeps the hidden field): text typed but not
// yet committed with Enter or a comma still counts when the field is left
// or its form is submitted. Capture, so the form's own submit handlers
// (dashboard.js) read the field after it.
function commitTags(box) {
  const inp = box?.querySelector("input:not([type=hidden])");
  const typed = inp?.value.split(/[,\n]/).map((v) => v.trim()).filter(Boolean) ?? [];
  if (!typed.length || !box.pbSet) return;
  box.pbSet([...box.pbValues(), ...typed]);
  inp.value = "";
}
if (globalThis.document) {
  document.addEventListener("submit", (e) => e.target.querySelectorAll?.("[data-tags]").forEach(commitTags), true);
  document.addEventListener("focusout", (e) => {
    if (e.target.matches?.("[data-tags] input:not([type=hidden])")) commitTags(e.target.closest("[data-tags]"));
  });
}
