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
  emptyColorsValid(root);
}

// ColorPicker: the kit marks a colour field invalid when it is empty, at
// bind and on change, but snap's colour fields may be left empty (the site
// colour then applies). Clear that mark, and let the form section take its
// snapshot again so a pristine page does not read "Unsaved changes".
function emptyColorsValid(root) {
  const fields = [...(root.querySelectorAll?.("[data-colorfield]") ?? [])];
  if (root instanceof Element && root.matches("[data-colorfield]")) fields.push(root);
  for (const f of fields) {
    const inp = f.querySelector("input:not([type=color])");
    const field = f.closest(".pb-field");
    if (!inp || !field) continue;
    const clear = () => { if (!inp.value.trim()) field.removeAttribute("data-invalid"); };
    if (!("emptyOk" in f.dataset)) {
      f.dataset.emptyOk = "";
      inp.addEventListener("change", clear); // after the kit's own listener
    }
    if (inp.value.trim()) continue;
    clear();
    const section = f.closest("[data-section]");
    const dirty = section?.querySelector(".pb-foot__status")?.dataset.state === "dirty";
    if (dirty && !section.querySelector("[data-invalid]")) section.pbSnapshot?.();
  }
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
