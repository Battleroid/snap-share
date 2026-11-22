// Paste and Link tabs of the composer, plus the options summary.

const optionsForm = document.getElementById("composer-options");

function readOptions() {
  const out = {};
  if (!optionsForm) return out;
  for (const [k, v] of new FormData(optionsForm)) if (v !== "") out[k] = String(v);
  return out;
}

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

function notice(status, title, url) {
  const n = el("div", "pb-notice");
  if (status) n.dataset.status = status;
  n.setAttribute("role", status === "down" ? "alert" : "status");
  const ic = el("span", `pb-icon pb-icon--status-${status || "info"}`);
  ic.setAttribute("aria-hidden", "true");
  n.append(ic, el("p", "pb-notice__title", title));
  if (url) {
    const body = el("p", "pb-notice__body");
    const a = el("a", "pb-link pb-mono", url.replace(/^https?:\/\//, ""));
    a.href = url;
    const b = el("button", "pb-btn pb-btn--ghost pb-btn--icon pb-btn--sm");
    b.type = "button";
    b.dataset.copy = "";
    b.dataset.copyValue = url;
    b.setAttribute("aria-label", "Copy link");
    b.append(el("span", "pb-icon pb-icon--copy"));
    body.append(a, " ", b);
    n.append(body);
  }
  return n;
}

function addRecent(item) {
  const list = document.querySelector("[data-recent]");
  if (!list) return;
  list.querySelector("[data-recent-empty]")?.remove();
  const row = el("div", "pb-filerow");
  const ic = el("span", `pb-icon pb-icon--${item.kind === "link" ? "link" : "file"}`);
  ic.setAttribute("aria-hidden", "true");
  const a = el("a", "pb-link pb-link--quiet", item.name);
  a.href = item.page_url;
  row.append(ic, a, el("span", "pb-filerow__meta", "just now"));
  list.prepend(row);
}

async function post(url, body) {
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify(body),
    credentials: "same-origin",
  });
  let data = null;
  try { data = await res.json(); } catch { /* empty */ }
  if (!res.ok) throw new Error((data && data.error) || `Request failed (${res.status}).`);
  return data;
}

function wire(form, url, build, done) {
  if (!form) return;
  const result = form.querySelector("[data-result]");
  const button = form.querySelector("button[type=submit]");
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    button.setAttribute("aria-busy", "true");
    button.disabled = true;
    try {
      const item = await post(url, { ...readOptions(), ...build(new FormData(form)) });
      result.replaceChildren(notice("ok", done, item.url));
      addRecent(item);
      form.querySelectorAll("input:not([type=hidden]),textarea").forEach((i) => { i.value = ""; });
    } catch (err) {
      result.replaceChildren(notice("down", err.message));
    } finally {
      button.removeAttribute("aria-busy");
      button.disabled = false;
    }
  });
}

wire(document.querySelector("[data-paste-form]"), "/api/v1/pastes", (f) => {
  const body = { content: String(f.get("content") || "") };
  for (const k of ["name", "lang"]) if (f.get(k)) body[k] = String(f.get(k));
  return body;
}, "Paste created");

wire(document.querySelector("[data-link-form]"), "/api/v1/links", (f) => {
  const body = { url: String(f.get("url") || "") };
  if (f.get("slug")) body.slug = String(f.get("slug"));
  return body;
}, "Link created");

// Summarise the options in the Disclosure's meta ("7 days · private · password").
const meta = document.querySelector("[data-options-meta]");
function summarise() {
  if (!optionsForm || !meta) return;
  const sel = optionsForm.querySelector("select[name=expires]");
  const parts = [sel ? sel.options[sel.selectedIndex].text : "never"];
  const vis = optionsForm.querySelector("select[name=visibility]");
  if (vis) parts.push(vis.value);
  if (optionsForm.querySelector("input[name=password]")?.value) parts.push("password");
  const naming = optionsForm.querySelector("input[name=naming]:checked");
  if (naming) parts.push(naming.value);
  meta.textContent = parts.join(" · ");
}
optionsForm?.addEventListener("input", summarise);
optionsForm?.addEventListener("change", summarise);
optionsForm?.addEventListener("submit", (e) => e.preventDefault());
summarise();
