// /x#<token>: delete an item with its deletion token (ShareX DeletionURL).
// The token lives in the fragment, so it never reaches access logs;
// fragment.js (a classic script in the page head) strips it from the address
// bar before the page is parsed, and it is sent only in POST bodies.

const root = document.querySelector("[data-token-x]");
if (root) run(root);

function notice(status, title) {
  const n = document.createElement("div");
  n.className = "pb-notice";
  if (status) n.dataset.status = status;
  n.setAttribute("role", status === "down" ? "alert" : "status");
  const ic = document.createElement("span");
  ic.className = `pb-icon pb-icon--status-${status || "info"}`;
  ic.setAttribute("aria-hidden", "true");
  const t = document.createElement("p");
  t.className = "pb-notice__title";
  t.textContent = title;
  n.append(ic, t);
  return n;
}

async function post(url, token) {
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify({ token }),
    credentials: "omit",
  });
  let data = null;
  try { data = await res.json(); } catch { /* empty */ }
  if (!res.ok) throw new Error((data && data.error) || `Request failed (${res.status}).`);
  return data;
}

async function run(root) {
  let fragment = globalThis.snapFragment;
  delete globalThis.snapFragment;
  if (typeof fragment !== "string") {
    fragment = location.hash.slice(1);
    history.replaceState(null, "", location.pathname);
  }
  let token = "";
  try {
    token = decodeURIComponent(fragment);
  } catch {
    // malformed escape: treated as no token
  }
  const q = (s) => root.querySelector(s);
  const message = q("[data-x-message]");
  const loading = q("[data-x-loading]");
  const found = q("[data-x-found]");
  if (!token) {
    loading.remove();
    message.replaceChildren(notice("down", "This deletion link is not valid."));
    return;
  }
  try {
    const it = await post("/api/v1/delete/lookup", token);
    q("[data-x-name]").textContent = it.name;
    q("[data-x-type]").textContent = it.type;
    q("[data-x-size]").textContent = it.size_text;
    q("[data-x-created]").textContent = String(it.created_at).slice(0, 10);
    found.hidden = false;
  } catch (err) {
    message.replaceChildren(notice("down", err.message));
  } finally {
    loading.remove();
  }
  q("[data-x-delete]").addEventListener("click", async (e) => {
    const b = e.currentTarget;
    b.disabled = true;
    try {
      await post("/api/v1/delete", token);
      found.remove();
      message.replaceChildren(notice("ok", "Deleted. The link no longer works."));
    } catch (err) {
      b.disabled = false;
      message.replaceChildren(notice("down", err.message));
    }
  });
}
