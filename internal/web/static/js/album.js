// Album page tools for the owner: reorder the members (the kit Sortable:
// drag a tile's handle, or focus it and use Space and the arrows; each
// tile's menu has Move earlier and Move later too) or take one out of the
// album (POST /api/v1/albums/{id}/items). The member item itself is not
// changed.

const root = document.querySelector("[data-album]");
const list = root?.querySelector("[data-sortable]");
if (list) init(root, list);

function init(root, list) {
  const id = root.dataset.album;
  const members = () => [...list.querySelectorAll(":scope > [data-member]")];
  const ids = () => members().map((li) => Number(li.dataset.member));
  let saved = ids(); // the order the server has

  async function change(body) {
    try {
      const res = await fetch(`/api/v1/albums/${id}/items`, {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        body: JSON.stringify(body),
      });
      if (res.ok) return null;
      const data = await res.json().catch(() => null);
      return data?.error || `Request failed (${res.status}).`;
    } catch {
      return "The server could not be reached.";
    }
  }

  function fail(msg) {
    let n = root.querySelector("[data-album-error]");
    if (!n) {
      n = document.createElement("div");
      n.className = "pb-notice";
      n.dataset.status = "down";
      n.dataset.albumError = "";
      n.setAttribute("role", "alert");
      const icon = document.createElement("span");
      icon.className = "pb-icon pb-icon--status-down";
      icon.setAttribute("aria-hidden", "true");
      const t = document.createElement("p");
      t.className = "pb-notice__title";
      n.append(icon, t);
      list.closest("[data-lightbox]").before(n);
    }
    n.querySelector(".pb-notice__title").textContent = msg;
  }

  // Puts the tiles in the given order of member ids.
  function arrange(order) {
    const by = new Map(members().map((li) => [Number(li.dataset.member), li]));
    for (const m of order) if (by.has(m)) list.append(by.get(m));
  }

  // The first tile cannot move earlier, the last cannot move later.
  function syncMenus() {
    const all = members();
    all.forEach((li, i) => {
      const earlier = li.querySelector('[data-row-action="album-earlier"]');
      const later = li.querySelector('[data-row-action="album-later"]');
      if (earlier) earlier.disabled = i === 0;
      if (later) later.disabled = i === all.length - 1;
    });
  }

  // Saves the order on the page; on failure the tiles go back.
  async function saveOrder() {
    const order = ids();
    if (order.join() === saved.join()) return true;
    const err = await change({ order });
    if (err) {
      arrange(saved);
      fail(err);
    } else {
      saved = order;
      root.querySelector("[data-album-error]")?.remove();
    }
    syncMenus();
    return !err;
  }

  // The kit Sortable has moved a tile (drag, or the keyboard on its handle).
  list.addEventListener("reorder", () => { saveOrder(); });

  list.addEventListener("click", async (e) => {
    const b = e.target instanceof Element ? e.target.closest("[data-row-action^='album-']") : null;
    if (!b) return;
    const li = b.closest("[data-member]");
    b.closest("[popover]")?.hidePopover();
    const menu = li.querySelector("[popovertarget]");
    if (b.dataset.rowAction === "album-remove") {
      const member = Number(li.dataset.member);
      const err = await change({ remove: [member] });
      if (err) return fail(err);
      li.remove();
      saved = saved.filter((m) => m !== member);
      if (!saved.length) return location.reload(); // the empty state
      syncMenus();
      return;
    }
    const all = members();
    const i = all.indexOf(li);
    const j = i + (b.dataset.rowAction === "album-earlier" ? -1 : 1);
    if (j < 0 || j >= all.length) return;
    if (j < i) list.insertBefore(li, all[j]);
    else all[j].after(li);
    await saveOrder();
    menu?.focus();
  });
  syncMenus();
}
