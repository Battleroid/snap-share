// Token pages (/verify, /reset, /invite). Emailed links carry the token in the
// URL fragment (#t=...), which browsers never send to the server, so it stays
// out of access logs. fragment.js, a classic script in the page head, removes
// it from the address bar and history before the page is parsed; this module
// picks it up and hands it to the server with a same-origin POST.
//
// Markup: an element with [data-token-page] and
//   data-mode="post": POST token to data-action; the JSON answer is
//     {redirect} (go there), {message, next} (show "done") or {error} (show "failed").
//   data-mode="confirm": POST token to data-action, which only checks it and
//     answers {confirm: "1", ...fields}; show "confirm" with each
//     [data-field=name] filled in, and POST token plus confirm=1 only when
//     the [data-confirm] button is clicked (a mail scanner that opens the
//     page changes nothing). The second answer is handled like "post".
//   data-mode="form": put the token into input[name=token] and show "form". With
//     data-check="/path" the token is first POSTed there (answer {valid}, or an
//     error): a link that no longer works shows "failed" instead of the form;
//     if the check itself cannot be made, the form shows and the submit decides.
//   data-initial: a state the server already chose (shown when no token is in the URL).
// Children with [data-state=busy|missing|failed|done|form|sent|confirm] are shown one at a time;
// a data-state may list several, separated by spaces (a foot link for two states).
// Showing "failed" or "done" moves focus to the way out in its result.

const root = document.querySelector("[data-token-page]");

function readToken() {
  // fragment.js stashed the fragment; read it ourselves if it did not run.
  let fragment = globalThis.snapFragment;
  delete globalThis.snapFragment;
  if (typeof fragment !== "string") {
    fragment = location.hash.slice(1);
    if (location.hash) history.replaceState(history.state, "", location.pathname + location.search);
  }
  return new URLSearchParams(fragment).get("t") || "";
}

function show(state, message) {
  for (const el of root.querySelectorAll("[data-state]")) el.hidden = !el.dataset.state.split(/s+/).includes(state);
  if (message !== undefined) {
    const target = root.querySelector(`[data-state~="${state}"] [data-message]`);
    if (target) target.textContent = message;
  }
  if (state === "failed" || state === "done") root.querySelector(`[data-state~="${state}"] .pb-result__actions a`)?.focus();
}

// check asks the server whether a reset link still works (POST data-check).
// Only a definite "no" (404) counts as no: if the answer can't be had, the
// form shows and the submit decides.
async function check(token) {
  show("busy");
  try {
    const res = await fetch(root.dataset.check, {
      method: "POST",
      body: new URLSearchParams({ token }),
      headers: { Accept: "application/json" },
      credentials: "same-origin",
      redirect: "error",
    });
    return res.status !== 404;
  } catch {
    return true;
  }
}

const isPath = (p) => typeof p === "string" && p.startsWith("/") && !p.startsWith("//") && !p.startsWith("/\\");

async function post(token, extra = {}) {
  show("busy");
  let res, data = {};
  try {
    res = await fetch(root.dataset.action, {
      method: "POST",
      body: new URLSearchParams({ token, ...extra }),
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
  if (data.confirm === "1") {
    for (const el of root.querySelectorAll("[data-field]")) el.textContent = data[el.dataset.field] || "—";
    show("confirm");
    const button = root.querySelector("[data-confirm]");
    button?.addEventListener("click", () => post(token, { confirm: "1" }), { once: true });
    button?.focus();
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
    const showForm = () => {
      show("form");
      root.querySelector('[data-state="form"] input:not([type="hidden"])')?.focus();
    };
    if (root.dataset.check) check(token).then((ok) => (ok ? showForm() : show("failed")));
    else showForm();
  } else {
    post(token);
  }
}
