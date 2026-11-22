// Share pages: the Markdown Rendered | Source toggles (one per Markdown
// file; a gist page can have several).

for (const toggle of document.querySelectorAll("[data-md-toggle]")) {
  const rendered = document.getElementById(toggle.dataset.mdRendered);
  const source = document.getElementById(toggle.dataset.mdSource);
  if (!rendered || !source) continue;
  toggle.addEventListener("change", (e) => {
    const src = e.target.value === "source";
    rendered.hidden = src;
    source.hidden = !src;
  });
}
