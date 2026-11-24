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
export const SKIP = ["strength", "table", "inbox"];

// init binds the kit hooks under root.
export function init(root = document) {
  pbInit(root, { skip: SKIP });
}
