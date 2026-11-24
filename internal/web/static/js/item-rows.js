// DOM builders shared by the composer's scripts (upload.js, composer.js): the
// finished-item result row, the Recent row, and the pieces they are made of.
// They write the same markup as _partials/page.html (item.share,
// item.visibility, media.lead) and kit ListItem, so a row built here looks
// like one rendered by the server. The share group and the processing marker
// come from the kit (pbShareLink, pbProcessing). arm() binds a built row to
// the kit (Copy link, the QR popover) once the caller has put it in the page.

import { pbProcessing, pbShareLink } from "../vendor/patchbay/patchbay.mjs";
import { init } from "./kit.js";

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

// TYPE_WORDS are the type words lists show, the same list as items.Types
// (internal/items/filter.go; TestTypeWordsMatchJS keeps them equal). The
// server decides which one an item is (list_type in the API's item JSON, the
// rows it renders), so a row built here reads the same as one rendered there.
export const TYPE_WORDS = ["image", "video", "audio", "text", "paste", "gist", "link", "album", "other"];

// typeWord is the lowercase type shown beside the size ("image", "paste").
export function typeWord(item) {
  return TYPE_WORDS.includes(item.list_type) ? item.list_type : "other";
}

export function typeIcon(item) {
  if (item.kind === "link") return "link";
  if (item.kind === "album") return "folder";
  if (item.kind === "gist") return "terminal";
  return "file";
}

let seq = 0;

// fromHTML turns the kit's markup strings into nodes.
export function fromHTML(html) {
  const t = document.createElement("template");
  t.innerHTML = html.trim();
  return t.content;
}

// shareGroup is item.share: the kit ShareLink, Copy link and the QR popover,
// after the link text and Open when showLink is set. It is a filled
// [data-sharelink] span, so it looks like the one the server renders.
export function shareGroup(url, { showLink = false } = {}) {
  const id = `share-${++seq}`;
  const w = el("span");
  w.id = id;
  w.dataset.sharelink = url;
  if (showLink) w.dataset.showLink = "";
  w.innerHTML = pbShareLink({ url, id, showLink });
  return w;
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

// processingAttrs are what a pending marker polls with: the item's media
// fragment, which swaps in the thumbnail (or "no preview") once the job is over.
export function processingAttrs(item) {
  return { "hx-get": `/dashboard/items/${item.id}/media?as=lead`, "hx-trigger": "every 5s", "hx-swap": "outerHTML" };
}

// processing is the owner's marker for a preview still being made (the media.lead
// partial): the kit's media state, polling the item's media fragment.
export function processing(item) {
  return fromHTML(pbProcessing({ variant: "lead", state: "pending", attrs: processingAttrs(item) })).firstElementChild;
}

// arm binds markup built here once the caller has put it in the page: the kit
// hooks (Copy link, the QR popover) and, for a processing marker, htmx.
// Neither watches for new elements.
export function arm(node, item) {
  queueMicrotask(() => {
    init(node);
    if (item?.media_state === "pending") globalThis.htmx?.process(node);
  });
}

function lead(item, thumb) {
  if (item.media_state === "pending") return processing(item);
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
// showLink false leaves the link text and Open out.
export function resultRow(item, thumb, { showLink = true } = {}) {
  const li = el("li");
  const row = el("div", "pb-item app-result");
  const end = el("span", "pb-item__end app-item-end");
  end.append(shareGroup(item.url, { showLink }));
  const size = item.kind === "link" || item.kind === "album" ? "" : ` · ${humanBytes(item.size || 0)}`;
  row.append(lead(item, thumb || item.thumbnail_url), titleBlock(item, `${typeWord(item)}${size}`), end);
  li.append(row);
  arm(li, item);
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
  end.append(meta, shareGroup(item.page_url));
  row.append(lead(item, item.thumbnail_url), left, end);
  li.append(row);
  arm(li, item);
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
