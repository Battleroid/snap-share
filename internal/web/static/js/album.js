// Album page tools for the owner: reorder the members (the kit Sortable:
// drag a tile's handle, or focus it and use Space and the arrows; each
// tile's menu has Move earlier and Move later too) or take one out of the
// album (POST /api/v1/albums/{id}/items). The member item itself is not
// changed. Removing asks first (the kit's data-confirm on the menu item);
// both changes say so with a toast when they worked and with a notice above
// the tiles when they did not. Rename (tile menu) changes the member's name
// in place: its title, or a file's name when it has no title, so what is
// renamed is what the tile shows; the album heading renames the album.

import { pbNoticeInto, pbToast } from "../vendor/patchbay/patchbay.mjs";
import { startRename } from "./inline-rename.js";

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

  // A failure stays above the tiles until it is dismissed or the next
  // change works.
  function fail(msg) {
    let box = root.querySelector("[data-album-error]");
    if (!box) {
      box = document.createElement("div");
      box.dataset.albumError = "";
      list.closest("[data-imageviewer]").before(box);
    }
    pbNoticeInto(box, { variant: "down", title: msg, dismissible: true });
  }
  const clearError = () => root.querySelector("[data-album-error]")?.remove();

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
      clearError();
      pbToast({ status: "ok", title: "Order saved" });
    }
    syncMenus();
    return !err;
  }

  // The kit Sortable has moved a tile (drag, or the keyboard on its handle).
  list.addEventListener("pb:reorder", () => { saveOrder(); });

  list.addEventListener("click", async (e) => {
    const b = e.target instanceof Element ? e.target.closest("[data-row-action^='album-']") : null;
    if (!b) return;
    const li = b.closest("[data-member]");
    b.closest("[popover]")?.hidePopover();
    const menu = li.querySelector("[popovertarget]");
    if (b.dataset.rowAction === "album-rename") {
      renameTile(li, menu);
      return;
    }
    if (b.dataset.rowAction === "album-remove") {
      const member = Number(li.dataset.member);
      const err = await change({ remove: [member] });
      if (err) return fail(err);
      const name = li.querySelector(".pb-media__name")?.textContent || "The item";
      li.remove();
      clearError();
      saved = saved.filter((m) => m !== member);
      pbToast({ status: "ok", title: `Removed ${name} from the album` });
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

// renameTile opens the rename field in place of the tile's name. A saved
// name reaches every place the tile names its item.
function renameTile(li, menu) {
  const head = li.querySelector(".app-mtile__head");
  const link = head?.querySelector(".pb-media__name");
  if (!link) return;
  const field = li.dataset.renameField;
  startRename(head, {
    url: `/api/v1/items/${li.dataset.member}`,
    field,
    value: li.dataset.renameValue,
    label: `Rename ${link.textContent}`,
    hide: [link, head.querySelector(".app-mtile__dl")].filter(Boolean),
    onSaved: (data) => {
      const name = data.name || link.textContent;
      li.dataset.renameValue = data[field] ?? "";
      if (field === "title" && !data.title && data.kind === "file") {
        // A cleared title shows the file name again; rename that next time.
        li.dataset.renameField = "filename";
        li.dataset.renameValue = data.filename || "";
      }
      link.textContent = name;
      link.title = name;
      const frame = li.querySelector(".pb-media__frame");
      if (frame) {
        frame.dataset.name = name;
        frame.setAttribute("aria-label", name);
      }
      li.querySelector(".app-mtile__dl")?.setAttribute("aria-label", `Download ${name}`);
      li.querySelector(".pb-sort__handle")?.setAttribute("aria-label", `Reorder ${name}`);
      const remove = li.querySelector('[data-row-action="album-remove"]');
      if (remove) remove.dataset.confirm = `Remove ${name} from this album?`;
      menu?.setAttribute("aria-label", menu.getAttribute("aria-label")?.replace(/ for .*$/, ` for ${name}`) || "");
      pbToast({ status: "ok", title: `Renamed to ${name}` });
      menu?.focus();
    },
  });
}
