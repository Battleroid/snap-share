// The album page's Grid | List switch ([data-album-view], hidden until this
// runs). It swaps the layout in place. A visitor's choice goes in the URL
// (?view=list), so a reload or a shared link keeps it; the album's default
// leaves the URL clean. The owner's switch (data-album-view-save) sets the
// default visitors get, and goes back with a toast if that fails.

import { pbToast } from "../vendor/patchbay/patchbay.mjs";

// viewURL is href with ?view= set for view, or removed when it is def.
export function viewURL(href, view, def) {
  const u = new URL(href);
  if (view === def) u.searchParams.delete("view");
  else u.searchParams.set("view", view);
  return u.pathname + u.search + u.hash;
}

const bar = globalThis.document?.querySelector("[data-album-view]");
const grid = bar && document.querySelector(".app-album-grid");
if (bar && grid) init(bar, grid);

function init(bar, grid) {
  const save = bar.dataset.albumViewSave;
  let def = bar.dataset.albumViewDefault || "grid";
  const show = (view) => {
    grid.classList.toggle("app-album-grid--list", view === "list");
    for (const r of bar.querySelectorAll("input[name=album-view]")) r.checked = r.value === view;
  };
  bar.hidden = false;

  bar.addEventListener("change", async (e) => {
    const view = e.target.value;
    if (view !== "grid" && view !== "list") return;
    show(view);
    if (!save) {
      history.replaceState(history.state, "", viewURL(location.href, view, def));
      return;
    }
    const was = def;
    def = view;
    history.replaceState(history.state, "", viewURL(location.href, view, def));
    let err = null;
    try {
      const res = await fetch(save, {
        method: "PUT",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        body: JSON.stringify({ view }),
      });
      if (!res.ok) err = (await res.json().catch(() => null))?.error || `Request failed (${res.status}).`;
    } catch {
      err = "The server could not be reached.";
    }
    if (err) {
      def = was;
      show(was);
      pbToast({ status: "down", title: `The view was not saved: ${err}` });
      return;
    }
    pbToast({ status: "ok", title: view === "list" ? "Visitors now see a list" : "Visitors now see tiles" });
  });
}
