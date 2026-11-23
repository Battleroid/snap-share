// Patchbay controls that need script: DatePicker, TagInput, Combobox,
// ColorPicker, Toast, Notifications (inbox) and the script Dialog (confirm). Ported from the kit's reference scripts
// (docs/dev/patchbay/components/<Name>/example.html), with inline styles
// replaced by CSSOM so they run under the app's CSP.
// init(root) starts every control found under root; app.js calls it on load
// and after htmx swaps.

const pad = (n) => String(n).padStart(2, "0");
const iso = (d) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;

function parse(s) {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(s || "");
  if (!m) return null;
  const d = new Date(+m[1], m[2] - 1, +m[3]);
  return d.getMonth() === m[2] - 1 ? d : null;
}

// place puts a popover under (or above) its anchor, inside the viewport.
function place(pop, anchor) {
  const r = anchor.getBoundingClientRect();
  const w = pop.offsetWidth, h = pop.offsetHeight;
  const below = r.bottom + 6 + h <= innerHeight || r.top - 6 - h < 0;
  pop.style.setProperty("left", `${Math.max(8, Math.min(r.left, innerWidth - w - 8))}px`);
  pop.style.setProperty("top", `${below ? r.bottom + 6 : r.top - 6 - h}px`);
}

function follow(pop, anchor) {
  const track = () => { if (pop.matches(":popover-open")) place(pop, anchor); };
  addEventListener("scroll", track, true);
  addEventListener("resize", track);
}

const MONTHS = ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"];
const DOW = ["Su", "Mo", "Tu", "We", "Th", "Fr", "Sa"];

// calendar returns a .pb-cal element for one date or a from/to range.
// Options: range, from, to, min, max, today, weekStart, onChange(v), onDone().
export function calendar(o = {}) {
  const range = !!o.range, ws = o.weekStart || 0, min = parse(o.min), max = parse(o.max);
  const today = (o.today && parse(o.today)) || new Date();
  today.setHours(0, 0, 0, 0);
  let from = parse(o.from), to = parse(o.to), hover = null, mode = "days";
  let focus = from || today, view = new Date(focus.getFullYear(), focus.getMonth(), 1);
  const el = document.createElement("div");
  el.className = "pb-cal";
  el.setAttribute("role", "dialog");
  el.setAttribute("aria-label", range ? "Choose dates" : "Choose a date");

  const same = (a, b) => !!(a && b && a.getTime() === b.getTime());
  const off = (d) => (min && d < min) || (max && d > max);
  const emit = () => o.onChange?.(range ? { from: from && iso(from), to: to && iso(to) } : from && iso(from));

  function pick(d) {
    if (!range) { from = d; emit(); render(); o.onDone?.(); return; }
    if (!from || to) { from = d; to = null; } else if (d < from) { to = from; from = d; } else { to = d; }
    focus = d; emit(); render();
    if (from && to) o.onDone?.();
  }
  function preset(days, month) {
    const t = new Date(today);
    if (month) { from = new Date(t.getFullYear(), t.getMonth(), 1); to = t; } else { to = t; from = new Date(t); from.setDate(from.getDate() - days + 1); }
    view = new Date(from.getFullYear(), from.getMonth(), 1); focus = to; emit(); render(); o.onDone?.();
  }
  function render() {
    const days = mode === "days";
    let h = "";
    if (range) h += '<div class="pb-cal__presets"><button type="button" class="pb-pill" data-p="1">today</button><button type="button" class="pb-pill" data-p="7">7d</button><button type="button" class="pb-pill" data-p="30">30d</button><button type="button" class="pb-pill" data-p="m">month</button></div>';
    h += `<div class="pb-cal__head"><button type="button" class="pb-cal__nav" data-nav="-1" aria-label="${days ? "Previous month" : "Previous year"}"><span class="pb-icon pb-icon--chevron-right pb-icon--flip"></span></button>` +
      `<button type="button" class="pb-cal__title" data-mode aria-label="${days ? "Choose month" : "Back to days"}">${days ? MONTHS[view.getMonth()] + " " + view.getFullYear() : view.getFullYear()}<span class="pb-icon pb-icon--chevron-down"></span></button>` +
      `<button type="button" class="pb-cal__nav" data-nav="1" aria-label="${days ? "Next month" : "Next year"}"><span class="pb-icon pb-icon--chevron-right"></span></button></div>`;
    if (days) {
      h += '<div class="pb-cal__grid">';
      for (let i = 0; i < 7; i++) h += `<span class="pb-cal__dow" aria-hidden="true">${DOW[(i + ws) % 7]}</span>`;
      const start = new Date(view);
      start.setDate(1 - ((view.getDay() - ws + 7) % 7));
      const end = to || (range && from && hover && hover > from ? hover : null);
      for (let k = 0; k < 42; k++) {
        const d = new Date(start);
        d.setDate(start.getDate() + k);
        const sel = same(d, from) || same(d, to);
        let a = ` data-d="${iso(d)}" tabindex="${same(d, focus) ? 0 : -1}" aria-pressed="${sel}"`;
        if (d.getMonth() !== view.getMonth()) a += " data-outside";
        if (same(d, today)) a += ' aria-current="date"';
        if (range && from && end) {
          if (d > from && d < end) a += " data-in-range";
          if (same(d, from)) a += " data-start";
          if (same(d, end)) a += " data-end";
        } else if (sel) a += " data-start data-end";
        if (off(d)) a += " disabled";
        a += ` aria-label="${d.toLocaleDateString(undefined, { weekday: "long", year: "numeric", month: "long", day: "numeric" })}"`;
        h += `<button type="button" class="pb-cal__day"${a}>${d.getDate()}</button>`;
      }
      h += "</div>";
    } else {
      h += '<div class="pb-cal__months">';
      MONTHS.forEach((m, i) => {
        const cur = today.getFullYear() === view.getFullYear() && today.getMonth() === i;
        const s = from && from.getFullYear() === view.getFullYear() && from.getMonth() === i;
        h += `<button type="button" class="pb-cal__month" data-m="${i}"${cur ? ' aria-current="true"' : ""} aria-pressed="${!!s}">${m.slice(0, 3)}</button>`;
      });
      h += "</div>";
    }
    const sum = range ? (from && to ? `${Math.round((to - from) / 864e5) + 1} days` : from ? "pick an end date" : "pick a start date") : "";
    h += `<div class="pb-cal__foot"><button type="button" class="pb-btn pb-btn--ghost pb-btn--sm" data-clear>Clear</button><span class="pb-cal__summary">${sum}</span><button type="button" class="pb-btn pb-btn--ghost pb-btn--sm" data-today>Today</button></div>`;
    el.innerHTML = h;
  }
  const focusDay = (v) => el.querySelector(`[data-d="${v}"]`)?.focus();
  el.addEventListener("click", (e) => {
    const b = e.target.closest("button");
    if (!b) return;
    if (b.dataset.d) { pick(parse(b.dataset.d)); focusDay(b.dataset.d); }
    else if (b.dataset.nav) {
      const n = +b.dataset.nav;
      view = mode === "days" ? new Date(view.getFullYear(), view.getMonth() + n, 1) : new Date(view.getFullYear() + n, view.getMonth(), 1);
      focus = new Date(view); render();
    } else if (b.hasAttribute("data-mode")) {
      mode = mode === "days" ? "months" : "days"; render();
      el.querySelector(mode === "days" ? '.pb-cal__day[tabindex="0"]' : ".pb-cal__month")?.focus();
    } else if (b.dataset.m) {
      view = new Date(view.getFullYear(), +b.dataset.m, 1); focus = new Date(view); mode = "days"; render();
      el.querySelector('.pb-cal__day[tabindex="0"]')?.focus();
    } else if (b.dataset.p) { b.dataset.p === "m" ? preset(0, true) : preset(+b.dataset.p); }
    else if (b.hasAttribute("data-clear")) { from = to = null; emit(); render(); }
    else if (b.hasAttribute("data-today")) {
      view = new Date(today.getFullYear(), today.getMonth(), 1); focus = new Date(today);
      if (!range) pick(new Date(today)); else render();
    }
  });
  el.addEventListener("mouseover", (e) => {
    const b = e.target.closest("[data-d]");
    if (!range || !from || to || !b) return;
    const d = parse(b.dataset.d);
    if (!same(d, hover)) { hover = d; render(); el.querySelector(`[data-d="${b.dataset.d}"]`)?.focus({ preventScroll: true }); }
  });
  el.addEventListener("keydown", (e) => {
    const b = e.target.closest("[data-d]");
    if (!b) return;
    const d = parse(b.dataset.d), k = e.key;
    const step = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -7, ArrowDown: 7 }[k];
    if (step) d.setDate(d.getDate() + step);
    else if (k === "Home") d.setDate(d.getDate() - ((d.getDay() - ws + 7) % 7));
    else if (k === "End") d.setDate(d.getDate() + 6 - ((d.getDay() - ws + 7) % 7));
    else if (k === "PageUp" || k === "PageDown") d.setMonth(d.getMonth() + (k === "PageUp" ? -1 : 1) * (e.shiftKey ? 12 : 1));
    else return;
    e.preventDefault();
    focus = d; view = new Date(d.getFullYear(), d.getMonth(), 1);
    if (range && from && !to) hover = d;
    render(); focusDay(iso(d));
  });
  el.pbSet = (f, t) => {
    from = parse(f); to = parse(t);
    if (from) { focus = from; view = new Date(from.getFullYear(), from.getMonth(), 1); }
    render();
  };
  el.pbFocus = () => el.querySelector('.pb-cal__day[tabindex="0"]')?.focus();
  render();
  return el;
}

// dateField wires a .pb-datefield or .pb-daterange to a popover calendar.
// The inputs keep yyyy-mm-dd and fire change.
export function dateField(field) {
  const range = field.classList.contains("pb-daterange");
  const ins = field.querySelectorAll("input");
  const btns = field.querySelectorAll("[data-cal]");
  let anchor = null;
  const cal = calendar({
    range, from: ins[0].value, to: range ? ins[1].value : null,
    min: field.dataset.min, max: field.dataset.max, today: field.dataset.today, weekStart: +(field.dataset.weekStart || 0),
    onChange(v) {
      if (range) { ins[0].value = v.from || ""; ins[1].value = v.to || ""; } else ins[0].value = v || "";
      ins.forEach((i) => i.dispatchEvent(new Event("change", { bubbles: true })));
    },
    onDone() { cal.hidePopover(); anchor?.focus(); },
  });
  cal.setAttribute("popover", "");
  cal.addEventListener("toggle", (e) => {
    if (e.newState === "closed" && anchor && (document.activeElement === document.body || cal.contains(document.activeElement))) anchor.focus();
  });
  (field.closest("label,.pb-field") || field).after(cal);
  follow(cal, field);
  const open = (b) => {
    anchor = b;
    cal.pbSet(ins[0].value, range ? ins[1].value : null);
    cal.showPopover(); place(cal, field); cal.pbFocus();
  };
  btns.forEach((b) => {
    b.setAttribute("aria-haspopup", "dialog");
    b.addEventListener("click", () => (cal.matches(":popover-open") ? cal.hidePopover() : open(b)));
  });
  ins.forEach((i) => {
    i.addEventListener("keydown", (e) => { if (e.altKey && e.key === "ArrowDown") { e.preventDefault(); open(btns[0]); } });
    i.addEventListener("change", () => {
      const f = i.closest(".pb-field,.pb-daterange");
      if (i.value && !parse(i.value)) f?.setAttribute("data-invalid", ""); else f?.removeAttribute("data-invalid");
    });
  });
}

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

// combo filters a .pb-combo listbox as the user types.
export function combo(c) {
  const inp = c.querySelector("input");
  const lb = c.querySelector(".pb-listbox");
  const opts = [...lb.querySelectorAll(".pb-option:not(.pb-option--empty)")];
  const empty = lb.querySelector(".pb-option--empty");
  const uid = "o" + Math.random().toString(36).slice(2, 7);
  let act = -1;
  opts.forEach((o, i) => { o.id = uid + i; o.dataset.label = o.textContent; });
  const vis = () => opts.filter((o) => !o.hidden);
  // label rewrites the option text, marking the typed part.
  function label(o, q) {
    const l = o.dataset.label, i = q ? l.toLowerCase().indexOf(q) : -1;
    const span = document.createElement("span");
    span.className = "pb-option__text";
    if (i < 0) span.textContent = l;
    else {
      const m = document.createElement("mark");
      m.textContent = l.slice(i, i + q.length);
      span.append(l.slice(0, i), m, l.slice(i + q.length));
    }
    o.replaceChildren(span);
  }
  function setAct(i) {
    const v = vis();
    opts.forEach((o) => o.removeAttribute("data-active"));
    act = i;
    if (i >= 0 && v[i]) { v[i].setAttribute("data-active", ""); inp.setAttribute("aria-activedescendant", v[i].id); v[i].scrollIntoView({ block: "nearest" }); }
    else inp.removeAttribute("aria-activedescendant");
  }
  function filter(q) {
    q = q.toLowerCase();
    opts.forEach((o) => { o.hidden = !!q && !o.dataset.label.toLowerCase().includes(q); label(o, q); });
    if (empty) empty.hidden = vis().length > 0;
    setAct(vis().length ? 0 : -1);
  }
  const open = (all) => { lb.hidden = false; inp.setAttribute("aria-expanded", "true"); filter(all ? "" : inp.value); };
  const close = () => { lb.hidden = true; inp.setAttribute("aria-expanded", "false"); };
  function choose(o) {
    opts.forEach((x) => x.setAttribute("aria-selected", String(x === o)));
    inp.value = o.dataset.value;
    close();
    inp.dispatchEvent(new Event("change", { bubbles: true }));
  }
  opts.forEach((o) => {
    o.setAttribute("aria-selected", String(o.dataset.value === inp.value));
    o.addEventListener("mousedown", (e) => { e.preventDefault(); choose(o); });
  });
  inp.addEventListener("input", () => open(false));
  inp.addEventListener("keydown", (e) => {
    const v = vis();
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      if (lb.hidden) { open(true); return; }
      setAct(Math.max(0, Math.min(v.length - 1, act + (e.key === "ArrowDown" ? 1 : -1))));
    } else if (e.key === "Enter" && !lb.hidden && v[act]) { e.preventDefault(); choose(v[act]); }
    else if (e.key === "Escape") close();
  });
  inp.addEventListener("blur", close);
  c.querySelector("[data-combo-toggle]")?.addEventListener("mousedown", (e) => {
    e.preventDefault();
    if (lb.hidden) { inp.focus(); open(true); } else close();
  });
}

const SWATCHES = [["#1f3fd6", "Blue"], ["#ff5b35", "Red"], ["#ffd23f", "Yellow"], ["#45d6a5", "Mint"], ["#0b7a5c", "Green"], ["#8a5200", "Ochre"],
  ["#c42d16", "Brick"], ["#8a2f9e", "Plum"], ["#17150f", "Ink"], ["#5b564c", "Grey"], ["#dcd6ca", "Sand"], ["#ffffff", "White"]];

// colorField wires .pb-input[data-colorfield]: a swatch button that opens kit
// colors and a custom picker, and a hex input. An empty input is allowed.
export function colorField(f) {
  const sw = f.querySelector(".pb-input__swatch");
  const inp = f.querySelector("input");
  const field = f.closest(".pb-field");
  const pop = document.createElement("div");
  pop.className = "pb-colorpop";
  pop.setAttribute("popover", "");
  pop.setAttribute("role", "dialog");
  pop.setAttribute("aria-label", "Choose a color");
  const eyebrow = (t) => Object.assign(document.createElement("span"), { className: "pb-eyebrow", textContent: t });
  const kit = document.createElement("div");
  kit.className = "pb-swatches";
  for (const [hex, name] of SWATCHES) {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "pb-swatch";
    b.dataset.hex = hex;
    b.setAttribute("aria-label", name);
    b.style.setProperty("--c", hex);
    kit.append(b);
  }
  const custom = document.createElement("div");
  custom.className = "pb-swatches";
  const lab = document.createElement("label");
  lab.className = "pb-swatch pb-swatch--custom";
  lab.setAttribute("aria-label", "Custom color");
  const plus = document.createElement("span");
  plus.className = "pb-icon pb-icon--plus";
  const picker = Object.assign(document.createElement("input"), { type: "color" });
  lab.append(plus, picker);
  custom.append(lab);
  pop.append(eyebrow("kit colors"), kit, eyebrow("custom"), custom);
  (f.closest("label,.pb-field") || f).after(pop);
  follow(pop, f);
  function set(h) {
    h = h.trim().replace(/^([0-9a-f]{6})$/i, "#$1").toLowerCase();
    const ok = h === "" || /^#[0-9a-f]{6}$/.test(h);
    field?.toggleAttribute("data-invalid", !ok);
    if (!ok) return;
    inp.value = h;
    sw.style.setProperty("--c", h || "transparent");
    if (h) picker.value = h;
    kit.querySelectorAll("[data-hex]").forEach((b) => b.setAttribute("aria-pressed", String(b.dataset.hex === h)));
  }
  const changed = () => inp.dispatchEvent(new Event("input", { bubbles: true }));
  sw.addEventListener("click", () => {
    if (pop.matches(":popover-open")) { pop.hidePopover(); return; }
    set(inp.value); pop.showPopover(); place(pop, f);
    (kit.querySelector('[aria-pressed="true"]') || kit.querySelector(".pb-swatch")).focus();
  });
  pop.addEventListener("click", (e) => {
    const b = e.target.closest("[data-hex]");
    if (b) { set(b.dataset.hex); changed(); pop.hidePopover(); sw.focus(); }
  });
  picker.addEventListener("input", () => { set(picker.value); changed(); });
  inp.addEventListener("change", () => set(inp.value));
  inp.form?.addEventListener("reset", () => setTimeout(() => set(inp.value)));
  set(inp.value);
}

const esc = (v) => String(v ?? "").replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/"/g, "&quot;");

// toast shows a message about the user's own action in the bottom-right stack.
// Options: title, body (text), status (info|ok|warn|down|busy), actions
// ([{label, onClick}], up to two), timeout (ms; 0 keeps it), ink.
// Returns the toast; toast.update(opts) changes it, toast.close() removes it.
export function toast(o = {}) {
  let box = document.body.querySelector(":scope > .pb-toasts");
  if (!box) {
    box = document.createElement("ol");
    box.className = "pb-toasts";
    box.setAttribute("aria-live", "polite");
    box.setAttribute("aria-label", "Notifications");
    document.body.append(box);
  }
  const t = document.createElement("li");
  box.append(t);
  const close = () => {
    if (!t.isConnected) return;
    t.setAttribute("data-leaving", "");
    setTimeout(() => t.remove(), matchMedia("(prefers-reduced-motion: reduce)").matches ? 0 : 160);
  };
  function draw(p) {
    Object.assign(o, p);
    const st = o.status || "info";
    const icon = { ok: "status-ok", warn: "status-warn", down: "status-down", info: "status-info", busy: "refresh" }[st];
    t.dataset.status = st;
    t.className = "pb-toast" + (o.ink ? " pb-toast--ink" : "");
    t.setAttribute("role", st === "down" ? "alert" : "status");
    const acts = (o.actions || []).map((a, i) => `<button type="button" class="pb-btn pb-btn--sm${i ? " pb-btn--ghost" : ""}" data-i="${i}">${esc(a.label)}</button>`).join("");
    t.innerHTML = `<span class="pb-icon pb-icon--${icon}${st === "busy" ? " pb-spin" : ""}" aria-hidden="true"></span><p class="pb-toast__title">${esc(o.title)}</p>` +
      (o.body ? `<p class="pb-toast__body">${esc(o.body)}</p>` : "") +
      (acts ? `<div class="pb-toast__actions">${acts}</div>` : "") +
      '<button type="button" class="pb-btn pb-btn--ghost pb-btn--icon pb-btn--sm" data-x aria-label="Dismiss"><span class="pb-icon pb-icon--close" aria-hidden="true"></span></button>';
    if (st !== "busy" && o.timeout !== 0) {
      const timer = document.createElement("span");
      timer.className = "pb-toast__timer";
      timer.style.setProperty("--t", `${o.timeout || (st === "down" ? 8000 : 5000)}ms`);
      timer.addEventListener("animationend", close);
      t.append(timer);
    }
  }
  t.addEventListener("click", (e) => {
    const b = e.target.closest("button");
    if (!b) return;
    if (b.hasAttribute("data-x")) { close(); return; }
    const a = o.actions?.[+b.dataset.i];
    a?.onClick?.(t);
    if (a?.close !== false) close();
  });
  t.addEventListener("keydown", (e) => { if (e.key === "Escape") close(); });
  while (box.children.length > 4) box.firstElementChild.remove();
  draw({});
  t.update = draw;
  t.close = close;
  return t;
}

// confirm asks a yes/no question in a small dialog and resolves true or false.
// Options: title, body (text), confirm, cancel, status, danger, typeToConfirm.
export function confirm(o = {}) {
  return new Promise((resolve) => {
    const d = document.createElement("dialog");
    d.className = "pb-dialog pb-dialog--sm";
    // The dialog is named by its title and described by its body.
    const uid = "pb-confirm-" + Math.random().toString(36).slice(2, 8);
    d.setAttribute("aria-labelledby", uid + "-t");
    if (o.body) d.setAttribute("aria-describedby", uid + "-b");
    const st = o.danger ? "down" : o.status || "";
    d.innerHTML = '<form method="dialog"><div class="pb-dialog__head">' +
      (st ? `<span class="pb-dialog__icon" data-status="${st}"><span class="pb-icon pb-icon--status-${st}" aria-hidden="true"></span></span>` : "") +
      `<div><h3 class="pb-dialog__title" id="${uid}-t">${esc(o.title || "Are you sure?")}</h3></div></div>` +
      `<div class="pb-dialog__body">${o.body ? `<p id="${uid}-b">${esc(o.body)}</p>` : ""}` +
      (o.typeToConfirm ? `<label class="pb-field"><span class="pb-field__label">type ${esc(o.typeToConfirm)} to confirm</span><span class="pb-input"><input autocomplete="off" spellcheck="false"></span></label>` : "") +
      `</div><div class="pb-dialog__foot"><button class="pb-btn pb-btn--ghost" value="cancel">${esc(o.cancel || "Cancel")}</button>` +
      `<button class="pb-btn ${o.danger ? "pb-btn--danger" : "pb-btn--primary"}" value="ok"${o.typeToConfirm ? " disabled" : ""}>${esc(o.confirm || "Confirm")}</button></div></form>`;
    document.body.append(d);
    const ok = d.querySelector("[value=ok]");
    const inp = d.querySelector("input");
    inp?.addEventListener("input", () => { ok.disabled = inp.value !== o.typeToConfirm; });
    d.addEventListener("close", () => { resolve(d.returnValue === "ok"); d.remove(); });
    d.showModal();
    (inp || d.querySelector("[value=cancel]")).focus();
  });
}

// inbox wires a Notifications wrapper: a .pb-bell and its .pb-inbox panel,
// a popover under the bell or pb-inbox--inline in the page. It keeps the
// bell's count and aria-label, marks a row read on click or Enter, moves
// between rows with ↑↓, and clears every row on [data-readall] (inside the
// wrapper). Unlike the kit's reference, the count is a number the caller
// can set (the server knows more unread than the panel shows). Options:
// unread (default: the unread rows), lazy (rows arrive later: no empty
// state until sync), onOpen(), onRead(row, wasUnread), onReadAll().
export function inbox(w, o = {}) {
  const bell = w.querySelector(".pb-bell");
  const panel = w.querySelector(".pb-inbox");
  const count = bell?.querySelector(".pb-bell__count");
  let unread = 0;
  function setUnread(n) {
    unread = Math.max(0, Math.trunc(Number(n)) || 0);
    if (!bell) return;
    count.textContent = unread ? String(unread) : "";
    count.toggleAttribute("data-zero", !unread);
    bell.setAttribute("aria-label", "Notifications" + (unread ? `, ${unread} unread` : ""));
  }
  function sync() {
    const e = panel.querySelector(".pb-inbox__empty");
    if (e) e.hidden = !!panel.querySelector(".pb-notif");
  }
  function placeUnder() {
    const r = bell.getBoundingClientRect();
    panel.style.setProperty("top", `${r.bottom + 8}px`);
    panel.style.setProperty("left", `${Math.max(8, Math.min(r.right - panel.offsetWidth, innerWidth - panel.offsetWidth - 8))}px`);
  }
  if (bell && panel.hasAttribute("popover")) {
    if (!bell.hasAttribute("popovertarget")) bell.addEventListener("click", () => panel.togglePopover());
    panel.addEventListener("toggle", (e) => {
      if (e.newState !== "open") return;
      placeUnder();
      (panel.querySelector(".pb-notif") || panel).focus();
      o.onOpen?.();
    });
    const track = () => { if (panel.matches(":popover-open")) placeUnder(); };
    addEventListener("scroll", track, true);
    addEventListener("resize", track);
  }
  panel.addEventListener("click", (e) => {
    const n = e.target.closest(".pb-notif");
    if (!n) return;
    const was = n.hasAttribute("data-unread");
    if (was) { n.removeAttribute("data-unread"); setUnread(unread - 1); }
    o.onRead?.(n, was);
  });
  w.addEventListener("click", (e) => {
    if (!e.target.closest("[data-readall]")) return;
    e.preventDefault();
    panel.querySelectorAll(".pb-notif[data-unread]").forEach((x) => x.removeAttribute("data-unread"));
    setUnread(0);
    o.onReadAll?.();
  });
  panel.addEventListener("keydown", (e) => {
    const n = e.target.closest(".pb-notif");
    if (!n) return;
    if (e.key === "Enter") { e.preventDefault(); n.click(); }
    else if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      (e.key === "ArrowDown" ? n.nextElementSibling : n.previousElementSibling)?.focus();
    }
  });
  if (!o.lazy) sync();
  setUnread(o.unread ?? panel.querySelectorAll(".pb-notif[data-unread]").length);
  return { setUnread, unread: () => unread, sync, panel, bell };
}

// init starts every control under root once.
export function init(root = document) {
  const each = (sel, fn) => root.querySelectorAll(sel).forEach((el) => {
    if (el.dataset.pbReady) return;
    el.dataset.pbReady = "1";
    fn(el);
  });
  each(".pb-datefield, .pb-daterange", dateField);
  each("[data-tags]", tags);
  each("[data-combo]", combo);
  each("[data-colorfield]", colorField);
}
