// Token pages (/verify, /reset, /invite). Emailed links carry the token in the
// URL fragment (#t=...), which browsers never send to the server, so it stays
// out of access logs. This module reads it, removes it from the address bar
// and history, and hands it to the server with a same-origin POST.
//
// Markup: an element with [data-token-page] and
//   data-mode="post": POST token to data-action; the JSON answer is
//     {redirect} (go there), {message, next} (show "done") or {error} (show "failed").
//   data-mode="form": put the token into input[name=token] and show "form".
//   data-initial: a state the server already chose (shown when no token is in the URL).
// Children with [data-state=busy|missing|failed|done|form|sent] are shown one at a time.

const root = document.querySelector("[data-token-page]");

function readToken() {
  const params = new URLSearchParams(location.hash.slice(1));
  const token = params.get("t") || "";
  if (location.hash) history.replaceState(history.state, "", location.pathname + location.search);
  return token;
}

function show(state, message) {
  for (const el of root.querySelectorAll("[data-state]")) el.hidden = el.dataset.state !== state;
  if (message !== undefined) {
    const target = root.querySelector(`[data-state="${state}"] [data-message]`);
    if (target) target.textContent = message;
  }
}

const isPath = (p) => typeof p === "string" && p.startsWith("/") && !p.startsWith("//") && !p.startsWith("/\\");

async function post(token) {
  show("busy");
  let res, data = {};
  try {
    res = await fetch(root.dataset.action, {
      method: "POST",
      body: new URLSearchParams({ token }),
      headers: { Accept: "application/json" },
      credentials: "same-origin",
      redirect: "error",
    });
    data = await res.json().catch(() => ({}));
  } catch {
    show("failed", "Could not reach the server. Reload the page to try again.");
    return;
  }
  if (!res.ok) {
    show("failed", data.error || "Something went wrong.");
    return;
  }
  if (isPath(data.redirect)) {
    location.assign(data.redirect);
    return;
  }
  const next = root.querySelector("[data-next]");
  if (next && isPath(data.next)) next.href = data.next;
  show("done", data.message || "Done.");
}

if (root) {
  const token = readToken();
  const initial = root.dataset.initial;
  if (!token) {
    show(initial || "missing");
  } else if (root.dataset.mode === "form") {
    for (const input of root.querySelectorAll('input[name="token"]')) input.value = token;
    show("form");
    root.querySelector('[data-state="form"] input:not([type="hidden"])')?.focus();
  } else {
    post(token);
  }
}
