// Album page tools for the owner: move a member earlier or later, or take it
// out of the album (POST /api/v1/albums/{id}/items). The member item itself
// is not changed.

const root = document.querySelector("[data-album]");
if (root && root.querySelector("[data-album-remove]")) init(root);

function init(root) {
  const id = root.dataset.album;
  const list = root.querySelector(".app-gallery");

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

  function syncButtons() {
    const items = [...list.children];
    items.forEach((li, i) => {
      li.querySelector("[data-album-move='-1']").disabled = i === 0;
      li.querySelector("[data-album-move='1']").disabled = i === items.length - 1;
    });
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
      list.before(n);
    }
    n.querySelector(".pb-notice__title").textContent = msg;
  }

  list.addEventListener("click", async (e) => {
    const b = e.target instanceof Element ? e.target.closest("[data-album-move], [data-album-remove]") : null;
    if (!b) return;
    const li = b.closest("[data-member]");
    const member = Number(li.dataset.member);
    if (b.hasAttribute("data-album-remove")) {
      const err = await change({ remove: [member] });
      if (err) return fail(err);
      li.remove();
      syncButtons();
      return;
    }
    const items = [...list.children];
    const i = items.indexOf(li);
    const j = i + Number(b.dataset.albumMove);
    if (j < 0 || j >= items.length) return;
    [items[i], items[j]] = [items[j], items[i]];
    const err = await change({ order: items.map((x) => Number(x.dataset.member)) });
    if (err) return fail(err);
    if (j < i) list.insertBefore(li, items[i]);
    else list.insertBefore(items[i], li);
    syncButtons();
    b.focus();
  });
  syncButtons();
}
