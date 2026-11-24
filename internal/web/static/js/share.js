// Share pages (item, gist, album): the Markdown Rendered | Source toggles
// (one per Markdown file; a gist page can have several) and the kit's
// ImageViewer on [data-imageviewer] (the share page's image, the album's
// pictures and videos).

import pbInit from "../vendor/patchbay/patchbay.mjs";

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

// kit.js leaves the imageviewer hook out of its pbInit; bind it here (each
// element binds once, so this is harmless when kit.js binds it too).
pbInit(document, { only: ["imageviewer"] });
