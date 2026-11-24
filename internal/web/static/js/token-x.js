// /x#<token>: delete an item with its deletion token (ShareX DeletionURL).
// The token lives in the fragment, so it never reaches access logs;
// fragment.js (a classic script in the page head) strips it from the address
// bar before the page is parsed, and it is sent only in POST bodies.
//
// The page is the confirm step: it names the file (name, type, size, when
// it was uploaded) above one Delete button, and after deleting the same card
// says so (data-x-done) and offers the way on. A lookup or delete that
// fails shows data-x-failed with the server's sentence in [data-x-message].

const root = document.querySelector("[data-token-x]");
if (root) run(root);

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

// whenText: the upload time as a local date, "2026-11-22".
function whenText(value) {
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return String(value).slice(0, 10);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
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
  const loading = q("[data-x-loading]");
  const found = q("[data-x-found]");
  // Replace the card's body with one end state; its way on is a link.
  const end = (which, text) => {
    loading.remove();
    found.remove();
    q("[data-x-foot]")?.remove();
    const box = q(`[data-x-${which}]`);
    if (text !== undefined) q("[data-x-message]").textContent = text;
    box.hidden = false;
    box.querySelector("a")?.focus();
  };
  if (!token) return end("failed", "The link is incomplete. Open the whole deletion link.");
  try {
    const it = await post("/api/v1/delete/lookup", token);
    q("[data-x-name]").textContent = it.name;
    q("[data-x-type]").textContent = it.type;
    q("[data-x-size]").textContent = it.size_text;
    q("[data-x-created]").textContent = whenText(it.created_at);
    loading.remove();
    found.hidden = false;
  } catch (err) {
    return end("failed", err.message);
  }
  const button = q("[data-x-delete]");
  const errorLine = document.createElement("p");
  errorLine.className = "pb-field__error app-auth-note";
  errorLine.setAttribute("role", "alert");
  errorLine.hidden = true;
  button.after(errorLine);
  button.addEventListener("click", async () => {
    button.disabled = true;
    errorLine.hidden = true;
    try {
      await post("/api/v1/delete", token);
      end("done");
    } catch (err) {
      button.disabled = false;
      errorLine.textContent = err.message;
      errorLine.hidden = false;
    }
  });
}
