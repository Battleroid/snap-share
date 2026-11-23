// Share pages: the Markdown Rendered | Source toggles (one per Markdown
// file; a gist page can have several), and the image frame's actual-size
// toggle.

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

// An image fills its frame. When it is smaller than the frame (so filling it
// scales it up), a click toggles between filling the frame and its actual
// size; a larger image keeps the link to the full-size file. Modified clicks
// and pages without script follow the link.
for (const frame of document.querySelectorAll("a[data-zoom]")) {
  const img = frame.querySelector("img");
  if (!img) continue;
  const label = frame.getAttribute("aria-label");
  const smaller = () => img.naturalWidth > 0 && img.naturalWidth < frame.clientWidth && img.naturalHeight < frame.clientHeight;
  const sync = () => {
    if (smaller()) {
      frame.setAttribute("data-zoomable", "");
      frame.setAttribute("role", "button");
      frame.setAttribute("aria-label", "Actual size");
      frame.setAttribute("aria-pressed", String(frame.hasAttribute("data-actual")));
    } else {
      for (const a of ["data-zoomable", "data-actual", "role", "aria-pressed"]) frame.removeAttribute(a);
      frame.setAttribute("aria-label", label);
    }
  };
  frame.addEventListener("click", (e) => {
    if (!frame.hasAttribute("data-zoomable") || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    e.preventDefault();
    frame.toggleAttribute("data-actual");
    frame.setAttribute("aria-pressed", String(frame.hasAttribute("data-actual")));
  });
  frame.addEventListener("keydown", (e) => {
    if (e.key === " " && frame.hasAttribute("data-zoomable")) {
      e.preventDefault();
      frame.click();
    }
  });
  if (img.complete) sync();
  else img.addEventListener("load", sync);
  new ResizeObserver(sync).observe(frame);
}
