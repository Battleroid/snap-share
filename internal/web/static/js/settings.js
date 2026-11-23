// Behaviour for the Settings pages (profile, security, defaults). Stand-ins
// until the kit's patchbay.mjs lands (data-vars, pbSectionNav).
//
//   [data-vars="--w:4%;--c:var(--chart-1)"]  custom properties for the Usage bars (CSP forbids style=)
//   ul[data-section-nav]                      marks the section in view
//   [data-codes-download=<id>]                saves the recovery codes as a .txt file
//   [data-codes-print]                        prints only the recovery codes
//   [data-naming-preview]                     the Defaults "a new link looks like" line

for (const el of document.querySelectorAll("[data-vars]")) {
  for (const pair of el.dataset.vars.split(";")) {
    const i = pair.indexOf(":");
    if (i > 0) el.style.setProperty(pair.slice(0, i).trim(), pair.slice(i + 1).trim());
  }
}

// Section nav: aria-current on the link of the section nearest the top.
for (const nav of document.querySelectorAll("ul[data-section-nav]")) {
  const links = [...nav.querySelectorAll("a[href^='#']")];
  const sections = links.map((a) => document.getElementById(a.getAttribute("href").slice(1))).filter(Boolean);
  if (!sections.length || !("IntersectionObserver" in window)) continue;
  const mark = (id) => links.forEach((a) => {
    if (a.getAttribute("href") === "#" + id) a.setAttribute("aria-current", "true");
    else a.removeAttribute("aria-current");
  });
  mark(sections[0].id);
  const seen = new Set();
  const io = new IntersectionObserver((entries) => {
    for (const e of entries) (e.isIntersecting ? seen.add(e.target) : seen.delete(e.target));
    const top = sections.find((s) => seen.has(s));
    if (top) mark(top.id);
  }, { rootMargin: "0px 0px -60% 0px" });
  sections.forEach((s) => io.observe(s));
}

// Recovery codes: Download .txt and Print.
document.addEventListener("click", (e) => {
  const dl = e.target.closest("[data-codes-download]");
  if (dl) {
    const text = document.getElementById(dl.dataset.codesDownload)?.textContent ?? "";
    const a = document.createElement("a");
    a.href = URL.createObjectURL(new Blob([text.trim() + "\n"], { type: "text/plain" }));
    a.download = "recovery-codes.txt";
    document.body.append(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(a.href), 1000);
    return;
  }
  if (e.target.closest("[data-codes-print]")) {
    document.body.classList.add("app-print-codes");
    window.addEventListener("afterprint", () => document.body.classList.remove("app-print-codes"), { once: true });
    window.print();
  }
});

// Defaults: the link preview and the random-length field follow the naming card.
const preview = document.querySelector("[data-naming-preview]");
if (preview) {
  const form = preview.closest("form");
  const line = preview.closest("[data-naming-preview-line]");
  const length = form.querySelector("[data-random-length]");
  const words = "sky-plum-fox";
  const alphabet = "k7qe2xw9abcdefghjkmnpqrstuvwxyz";
  const update = () => {
    const scheme = form.querySelector("input[name=naming]:checked")?.value;
    const n = Math.min(32, Math.max(10, Number(form.elements.random_length.value) || 10));
    let example = alphabet.repeat(2).slice(0, n);
    if (scheme === "words") example = words;
    if (scheme === "original") example = "holiday.jpg";
    preview.textContent = `${location.host}/${example}`;
    line.hidden = false;
    length.hidden = scheme !== "random"; // hidden, still submitted
  };
  form.addEventListener("input", update);
  form.addEventListener("change", update);
  form.addEventListener("reset", () => setTimeout(update));
  update();
}

// Defaults: the chat-embed preview follows the site name and colour fields.
const embed = document.querySelector("[data-embed-preview]");
if (embed) {
  const form = embed.closest("form");
  const site = embed.querySelector("[data-embed-site]");
  const sync = () => {
    site.textContent = form.elements.embed_title.value.trim() || embed.dataset.siteDefault;
    const c = form.elements.embed_color.value.trim();
    if (/^#[0-9a-fA-F]{6}$/.test(c)) embed.style.setProperty("--embed-color", c);
    else embed.style.removeProperty("--embed-color");
  };
  form.addEventListener("input", sync);
  form.addEventListener("change", sync);
  form.addEventListener("reset", () => setTimeout(sync));
  sync();
}
