// New API key form: the date field matters only when "on a date" is chosen.
const select = document.getElementById("key-expires");
const date = document.getElementById("key-expires-on");
if (select && date) {
  const sync = () => {
    const on = select.value === "date";
    date.hidden = !on;
    date.querySelector("input")?.toggleAttribute("required", on);
  };
  select.addEventListener("change", sync);
  sync();
}

// Scope presets: "Upload only" ticks items:write alone, "Full access" the
// three item scopes. Ticking a box by hand clears the preset. Without script
// the boxes are the form.
const presets = document.querySelector("[data-scope-presets]");
if (presets) {
  const boxes = [...document.querySelectorAll('input[name="scope"]')];
  const radios = [...presets.querySelectorAll('input[name="scope-preset"]')];
  const sets = { upload: ["items:write"], full: ["items:read", "items:write", "items:delete"] };
  const sync = () => {
    const on = boxes.filter((b) => b.checked).map((b) => b.value).sort().join();
    for (const r of radios) r.checked = sets[r.value].slice().sort().join() === on;
  };
  presets.addEventListener("change", (e) => {
    const set = sets[e.target.value];
    if (!set) return;
    for (const b of boxes) b.checked = set.includes(b.value);
  });
  for (const b of boxes) b.addEventListener("change", sync);
  presets.hidden = false;
  sync();
}
