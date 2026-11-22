// Album lightbox: opens images and videos from the gallery in a modal
// <dialog>. Arrow keys and swipes move between them, Escape closes, focus
// stays inside while open and returns to the card on close. Anything else
// (and a modified click) opens the item's own page as a normal link.

const dlg = document.getElementById("lightbox");
if (dlg) init(dlg);

function init(dlg) {
  const stage = dlg.querySelector("[data-lightbox-stage]");
  const title = dlg.querySelector("[data-lightbox-title]");
  const pos = dlg.querySelector("[data-lightbox-pos]");
  const open = dlg.querySelector("[data-lightbox-open]");
  const prev = dlg.querySelector("[data-lightbox-prev]");
  const next = dlg.querySelector("[data-lightbox-next]");
  const close = dlg.querySelector("[data-lightbox-close]");
  let list = [];
  let at = 0;
  let opener = null;

  document.addEventListener("click", (e) => {
    const a = e.target instanceof Element ? e.target.closest("[data-lightbox]") : null;
    if (!a || dlg.contains(a) || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    e.preventDefault();
    list = [...document.querySelectorAll("[data-lightbox]")];
    opener = a;
    show(list.indexOf(a));
    dlg.showModal();
    close.focus();
  });

  function media(a) {
    if (a.dataset.lightbox === "video") {
      const v = document.createElement("video");
      v.controls = true;
      v.playsInline = true;
      v.preload = "metadata";
      if (a.dataset.poster) v.poster = a.dataset.poster;
      v.src = a.dataset.src;
      return v;
    }
    const img = document.createElement("img");
    img.alt = a.dataset.title || "";
    img.decoding = "async";
    img.src = a.dataset.src;
    return img;
  }

  function show(i) {
    if (!list.length) return;
    at = (i + list.length) % list.length;
    const a = list[at];
    stage.replaceChildren(media(a));
    title.textContent = a.dataset.title || "";
    pos.textContent = `${at + 1} of ${list.length}`;
    open.href = a.href;
    prev.disabled = next.disabled = list.length < 2;
  }

  prev.addEventListener("click", () => show(at - 1));
  next.addEventListener("click", () => show(at + 1));
  close.addEventListener("click", () => dlg.close());
  // A click on the backdrop (the dialog element itself) closes it.
  dlg.addEventListener("click", (e) => { if (e.target === dlg) dlg.close(); });

  dlg.addEventListener("keydown", (e) => {
    if (e.key === "ArrowLeft") { e.preventDefault(); show(at - 1); }
    else if (e.key === "ArrowRight") { e.preventDefault(); show(at + 1); }
    else if (e.key === "Tab") trap(e);
  });

  // Keep Tab inside the dialog.
  function trap(e) {
    const f = [...dlg.querySelectorAll("a[href], button:not([disabled]), video[controls], [tabindex]:not([tabindex='-1'])")]
      .filter((el) => el.getClientRects().length);
    if (!f.length) return;
    const first = f[0];
    const last = f[f.length - 1];
    if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
    else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
  }

  // Swipe left or right on touch screens.
  let startX = null;
  stage.addEventListener("pointerdown", (e) => { if (e.pointerType !== "mouse") startX = e.clientX; });
  stage.addEventListener("pointerup", (e) => {
    if (startX === null) return;
    const dx = e.clientX - startX;
    startX = null;
    if (Math.abs(dx) > 40) show(dx < 0 ? at + 1 : at - 1);
  });
  stage.addEventListener("pointercancel", () => { startX = null; });

  dlg.addEventListener("close", () => {
    stage.replaceChildren(); // stops a playing video
    opener?.focus();
    opener = null;
  });
}
