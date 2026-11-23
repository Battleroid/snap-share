// data-vars: CSS custom properties for the kit's data displays (storage bar
// widths, meter fills, dashboard spans), set with setProperty because the CSP
// forbids style attributes. data-vars="--w:12%;--c:var(--chart-1)". Only a
// short list of names and plain values (a number, a percentage, or a kit
// token as var(--name)) is applied. The kit's own data-vars hook does the
// same once the kit JS is vendored; applying a value twice is harmless.

const NAMES = new Set(["--w", "--c", "--value", "--span", "--p", "--at"]);
const VALUE = /^(?:\d+(?:\.\d+)?%?|var\(--[a-z0-9-]+\))$/;

// parseVars returns the [name, value] pairs of a data-vars string that pass.
export function parseVars(text) {
  const out = [];
  for (const decl of String(text ?? "").split(";")) {
    const i = decl.indexOf(":");
    if (i < 0) continue;
    const name = decl.slice(0, i).trim();
    const value = decl.slice(i + 1).trim();
    if (NAMES.has(name) && VALUE.test(value)) out.push([name, value]);
  }
  return out;
}

export function applyVars(root = document) {
  const els = root.matches?.("[data-vars]") ? [root] : [];
  for (const el of [...els, ...root.querySelectorAll("[data-vars]")]) {
    for (const [name, value] of parseVars(el.dataset.vars)) el.style.setProperty(name, value);
  }
}

if (globalThis.document) {
  applyVars();
  document.addEventListener("htmx:afterSwap", (e) => applyVars(e.target));
}
