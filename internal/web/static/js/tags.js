// TagInput for snap's pb.tags partial. The kit's own tags hook (skipped in
// kit.js) keeps no form value; this one keeps a hidden input in step, so
// forms submit the tags as before, and box.pbSet(list) replaces them
// (js/dashboard.js clears the Tags dialog with it).

// tags turns span.pb-tags into chips. The hidden input inside holds the
// values joined by data-sep (default ", "), so forms submit them as before;
// box.pbSet(values) replaces the chips.
export function tags(box) {
  const inp = box.querySelector("input:not([type=hidden])");
  const out = box.querySelector("input[type=hidden]");
  const sep = box.dataset.sep === "lines" ? "\n" : ", ";
  const values = () => [...box.querySelectorAll(".pb-tag")].map((t) => t.firstChild.textContent);
  const sync = () => {
    if (out) out.value = values().join(sep);
    box.dispatchEvent(new Event("change", { bubbles: true }));
  };
  function add(v, quiet) {
    v = v.trim().replace(/,$/, "");
    if (!v || values().includes(v)) return;
    const t = document.createElement("span");
    t.className = "pb-tag";
    t.textContent = v;
    const b = document.createElement("button");
    b.type = "button";
    b.setAttribute("aria-label", `Remove ${v}`);
    const ic = document.createElement("span");
    ic.className = "pb-icon pb-icon--close";
    ic.setAttribute("aria-hidden", "true");
    b.append(ic);
    t.append(b);
    box.insertBefore(t, inp);
    if (!quiet) sync();
  }
  const commit = () => { if (inp.value.trim()) { inp.value.split(/[,\n]/).forEach((v) => add(v)); inp.value = ""; } };
  box.addEventListener("click", (e) => {
    const b = e.target.closest(".pb-tag button");
    if (b) { b.parentNode.remove(); sync(); inp.focus(); } else if (e.target === box) inp.focus();
  });
  inp.addEventListener("keydown", (e) => {
    if ((e.key === "Enter" || e.key === "," || e.key === "Tab") && inp.value.trim()) { e.preventDefault(); commit(); }
    else if (e.key === "Backspace" && !inp.value) { box.querySelector(".pb-tag:last-of-type")?.remove(); sync(); }
  });
  inp.addEventListener("blur", commit);
  inp.addEventListener("paste", (e) => {
    const t = e.clipboardData.getData("text");
    if (/[,\n]/.test(t)) { e.preventDefault(); t.split(/[,\n]/).forEach((v) => add(v)); }
  });
  // Typed text counts when the form is submitted without leaving the field.
  inp.form?.addEventListener("submit", commit, true);
  box.pbSet = (list) => {
    box.querySelectorAll(".pb-tag").forEach((t) => t.remove());
    list.forEach((v) => add(v, true));
    if (out) out.value = values().join(sep);
  };
  if (out?.value) box.pbSet(out.value.split(sep === "\n" ? /\n/ : /,/).map((v) => v.trim()).filter(Boolean));
}
