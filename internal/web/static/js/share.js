// Share pages: the Markdown Rendered | Source toggle.

const toggle = document.querySelector("[data-md-toggle]");
if (toggle) {
  const rendered = document.getElementById("md-rendered");
  const source = document.getElementById("md-source");
  toggle.addEventListener("change", (e) => {
    const src = e.target.value === "source";
    rendered.hidden = src;
    source.hidden = !src;
  });
}
