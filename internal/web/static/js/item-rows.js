// DOM builders shared by the composer's scripts (upload.js, composer.js): the
// finished-item result row, the Recent row, and the pieces they are made of.
// They write the same markup as _partials/page.html (item.share,
// item.visibility) and kit ListItem / UploadQueue, so a row built here looks
// like one rendered by the server.

export function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

export function icon(name) {
  const i = el("span", `pb-icon pb-icon--${name}`);
  i.setAttribute("aria-hidden", "true");
  return i;
}

export function humanBytes(n) {
  if (n < 1000) return `${n} B`;
  const units = "KMGTPE";
  let v = n / 1000;
  let i = 0;
  while (v >= 1000 && i < units.length - 1) { v /= 1000; i++; }
  return `${v >= 100 ? v.toFixed(0) : v.toFixed(1)} ${units[i]}B`;
}

// shortLink is a link without its scheme, for link text.
export const shortLink = (url) => String(url).replace(/^https?:\/\//, "");

// typeWord is the lowercase type shown beside the size ("image", "paste").
export function typeWord(item) {
  if (item.kind && item.kind !== "file") return item.kind === "paste" ? (item.lang && item.lang !== "plaintext" ? item.lang : "text") : item.kind;
  const mime = String(item.type || "");
  const major = mime.split("/")[0];
  if (["image", "video", "audio", "text"].includes(major)) return major;
  const ext = String(item.name || "").includes(".") ? item.name.split(".").pop().toLowerCase() : "";
  return ext || "file";
}

export function typeIcon(item) {
  if (item.kind === "link") return "link";
  if (item.kind === "album") return "folder";
  if (item.kind === "gist") return "terminal";
  return "file";
}

let seq = 0;

// button builds a kit button: opts.small, icon-only (opts.label as aria-label).
function button(label, iconName, opts = {}) {
  const b = el("button", `pb-btn${opts.small === false ? "" : " pb-btn--sm"}${opts.iconOnly ? " pb-btn--icon" : ""}`);
  b.type = "button";
  if (iconName) b.append(icon(iconName));
  if (opts.iconOnly) b.setAttribute("aria-label", label);
  else b.append(el("span", "", label));
  return b;
}

// shareGroup is item.share: Copy link, Open (with showLink) and the QR popover,
// optionally after the link text. name labels the group for assistive tech.
export function shareGroup(url, { showLink = false, name = "" } = {}) {
  const frag = document.createDocumentFragment();
  const id = `share-${++seq}`;
  const wrap = el("span", "app-share-link");
  if (showLink) {
    const a = el("a", "pb-link pb-link--quiet pb-mono app-share-link__url", shortLink(url));
    a.href = url;
    a.title = url;
    wrap.append(a);
  }
  const group = el("span", "pb-btngroup");
  if (name) { group.setAttribute("role", "group"); group.setAttribute("aria-label", `Link to ${name}`); }
  const copy = button("Copy link", "copy");
  copy.dataset.copy = "";
  copy.dataset.copyValue = url;
  group.append(copy);
  if (showLink) {
    const open = el("a", "pb-btn pb-btn--sm");
    open.href = url;
    open.append(icon("link-external"), "Open");
    group.append(open);
  }
  const qr = button(name ? `QR code for ${name}` : "QR code", "qr", { iconOnly: true });
  qr.setAttribute("popovertarget", `${id}-qr`);
  group.append(qr);
  wrap.append(group);

  const pop = el("div", "pb-popover");
  pop.id = `${id}-qr`;
  pop.setAttribute("popover", "");
  pop.setAttribute("role", "dialog");
  pop.setAttribute("aria-label", "QR code");
  const fig = el("figure", "pb-qr pb-qr--bare");
  fig.dataset.qr = url;
  fig.append(el("div", "pb-qr__code"), el("figcaption", "pb-qr__caption pb-mono app-qr-pop__link", url));
  pop.append(fig);
  frag.append(wrap, pop);
  return frag;
}

// visibility is item.visibility: muted unlisted / private, plus a yellow
// password badge.
export function visibility(vis, password) {
  const box = el("span", "app-vis");
  if (vis === "private" || vis === "unlisted") {
    const v = el("span", "app-vis__item");
    v.title = vis === "private" ? "Only you can open it" : "Anyone with the link can open it";
    v.append(icon(vis === "private" ? "eye-off" : "link"), vis);
    box.append(v);
  }
  if (password) {
    const p = el("span", "pb-badge pb-badge--solid");
    p.dataset.status = "warn";
    p.title = "Needs a password to open";
    p.append(icon("lock"), "password");
    box.append(p);
  }
  return box;
}

function lead(item, thumb) {
  const l = el("span", "pb-item__lead");
  if (thumb) {
    l.classList.add("pb-item__lead--thumb");
    const img = el("img");
    img.src = thumb;
    img.alt = "";
    img.loading = "lazy";
    l.append(img);
  } else {
    l.append(icon(typeIcon(item)));
  }
  return l;
}

function titleBlock(item, desc) {
  const t = el("span");
  t.append(el("span", "pb-item__title", item.name), el("span", "pb-item__desc pb-mono", desc));
  return t;
}

// resultRow is a finished item: name, type · size, then the link text and
// Copy link / Open / QR. thumb is a local preview of an image just uploaded.
export function resultRow(item, thumb) {
  const li = el("li");
  const row = el("div", "pb-item app-result");
  const end = el("span", "pb-item__end app-item-end");
  end.append(shareGroup(item.url, { showLink: true, name: item.name }));
  const size = item.kind === "link" || item.kind === "album" ? "" : ` · ${humanBytes(item.size || 0)}`;
  row.append(lead(item, thumb || item.thumbnail_url), titleBlock(item, `${typeWord(item)}${size}`), end);
  li.append(row);
  return li;
}

// recentRow is one row of the composer's Recent list, as the server draws
// it: thumb, name and type · size on the left; visibility, "just now" and
// the Copy link + QR group on the right.
export function recentRow(item) {
  const li = el("li");
  const row = el("div", "pb-item app-recent");
  const name = el("a", "pb-link pb-link--quiet pb-item__title", item.name);
  name.href = item.page_url;
  const left = el("span");
  const size = item.kind === "link" || item.kind === "album" ? "" : ` · ${humanBytes(item.size || 0)}`;
  left.append(name, el("span", "pb-item__desc pb-mono", `${typeWord(item)}${size}`));
  const end = el("span", "pb-item__end app-item-end");
  end.append(visibility(item.visibility, item.password));
  const meta = el("span", "pb-item__meta", "just now");
  end.append(meta, shareGroup(item.page_url, { name: item.name }));
  row.append(lead(item, item.thumbnail_url), left, end);
  li.append(row);
  return li;
}

// addRecent puts a created item at the top of the Recent list.
export function addRecent(item) {
  const list = document.querySelector("[data-recent]");
  if (!list) return;
  document.querySelector("[data-recent-empty]")?.remove();
  list.hidden = false;
  list.prepend(recentRow(item));
}

// notice builds a kit Notice. body may be a string or a node.
export function notice(status, title, body) {
  const n = el("div", "pb-notice");
  if (status) n.dataset.status = status;
  n.setAttribute("role", status === "down" ? "alert" : "status");
  n.append(icon(`status-${status || "info"}`), el("p", "pb-notice__title", title));
  if (body) {
    const p = el("p", "pb-notice__body");
    p.append(body);
    n.append(p);
  }
  return n;
}
