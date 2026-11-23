// The header bell and /notifications: the popover's list (htmx, loaded when
// it opens), live updates from /notifications/stream (Server-Sent Events),
// and reading. The layout loads this module only on pages with the bell.
// Rows from the stream are built with textContent; rows in the page come
// from the server's templates.

import { inbox } from "./patchbay.js";

const PANEL_ROWS = 20; // GET /notifications/panel
const PAGE_ROWS = 50; // a page of /notifications
const ICON = /^[a-z0-9-]+$/;
const STATUS = new Set(["info", "ok", "warn", "down"]);

// safeHref returns link when it is a path on this origin, and "" otherwise.
export function safeHref(link) {
  return typeof link === "string" && /^\/(?![/\\])/.test(link) ? link : "";
}

// rowElement builds a .pb-notif row for a stream entry.
export function rowElement(doc, e) {
  const el = (tag, cls, text) => {
    const n = doc.createElement(tag);
    n.className = cls;
    if (text !== undefined) n.textContent = String(text ?? "");
    return n;
  };
  const li = el("li", "pb-notif");
  li.dataset.id = String(Math.trunc(Number(e.id)) || 0);
  li.dataset.status = STATUS.has(e.status) ? e.status : "info";
  li.dataset.href = safeHref(e.link);
  if (e.unread) li.setAttribute("data-unread", "");
  li.setAttribute("tabindex", "0");
  const icon = el("span", "pb-icon pb-icon--" + (ICON.test(e.icon ?? "") ? e.icon : "bell"));
  icon.setAttribute("aria-hidden", "true");
  const time = el("time", "pb-notif__time", e.time);
  time.setAttribute("datetime", String(e.at ?? ""));
  li.append(icon, el("span", "pb-notif__title", e.title), time);
  if (e.body) li.append(el("span", "pb-notif__body", e.body));
  return li;
}

function parse(data) {
  try { return JSON.parse(data); } catch { return null; }
}

// post sends a same-origin POST and returns the JSON answer, or null.
async function post(url) {
  const ctl = new AbortController();
  const t = setTimeout(() => ctl.abort(), 3000);
  try {
    const res = await fetch(url, { method: "POST", headers: { Accept: "application/json" }, credentials: "same-origin", signal: ctl.signal });
    return res.ok ? await res.json() : null;
  } catch {
    return null;
  } finally {
    clearTimeout(t);
  }
}

function prepend(list, entry, max) {
  if (list.querySelector(`.pb-notif[data-id="${Math.trunc(Number(entry.id)) || 0}"]`)) return;
  list.prepend(rowElement(document, entry));
  while (list.children.length > max) list.lastElementChild.remove();
}

function start(bellWrap) {
  const panel = bellWrap.querySelector(".pb-inbox");
  const list = panel.querySelector(".pb-inbox__list");
  const empty = panel.querySelector(".pb-inbox__empty");
  const pageWrap = document.querySelector('[data-inbox="page"]');
  const pageList = pageWrap?.querySelector(".pb-inbox__list");
  let loaded = false;

  // A click marks the row read (and waits for it, so the next page counts
  // it), then follows its link.
  async function read(row, wasUnread) {
    const id = row.dataset.id;
    if (wasUnread && /^\d+$/.test(id ?? "")) {
      document.querySelectorAll(`.pb-notif[data-id="${id}"]`).forEach((x) => x.removeAttribute("data-unread"));
      const r = await post(`/notifications/${id}/read`);
      if (r) bell.setUnread(r.unread);
    }
    const href = safeHref(row.dataset.href);
    if (href) location.assign(href);
  }
  async function readAll() {
    document.querySelectorAll(".pb-notif[data-unread]").forEach((x) => x.removeAttribute("data-unread"));
    bell.setUnread(0);
    const r = await post("/notifications/read-all");
    if (r) bell.setUnread(r.unread);
  }
  const load = () => {
    loaded = false;
    if (empty) empty.hidden = true;
    list.dispatchEvent(new Event("inbox-open")); // hx-trigger on the list
  };

  const bell = inbox(bellWrap, {
    unread: Number(bellWrap.querySelector(".pb-bell")?.dataset.unread) || 0,
    lazy: true, onOpen: load, onRead: read, onReadAll: readAll,
  });
  list.addEventListener("htmx:afterSwap", () => {
    loaded = true;
    bell.sync();
    if (document.activeElement === panel) list.querySelector(".pb-notif")?.focus();
  });
  const page = pageWrap ? inbox(pageWrap, { onRead: read, onReadAll: readAll }) : null;
  // The page's Mark all read sits in its header, outside the inbox wrapper.
  document.querySelector(".app-inbox-page [data-readall]")?.addEventListener("click", (e) => {
    e.preventDefault();
    readAll();
  });

  // Live updates. EventSource reconnects by itself after the server ends a
  // stream; a refused stream (too many open, signed out) is tried again later.
  // A tab hidden for a minute lets its stream go (the server allows a few
  // per user) and catches up when it is shown again.
  let es = null;
  let timer = 0;
  let idle = 0;
  let backoff = 30_000;
  function disconnect() {
    clearTimeout(timer);
    es?.close();
    es = null;
  }
  function connect() {
    if (!window.EventSource || es || document.visibilityState === "hidden") return;
    es = new EventSource("/notifications/stream");
    es.addEventListener("open", () => { backoff = 30_000; });
    es.addEventListener("count", (m) => {
      const d = parse(m.data);
      if (!d) return;
      bell.setUnread(d.unread);
      if (d.resync && panel.matches(":popover-open")) load();
    });
    es.addEventListener("notification", (m) => {
      const d = parse(m.data);
      if (!d?.entry) return;
      bell.setUnread(d.unread);
      if (loaded && panel.matches(":popover-open")) { prepend(list, d.entry, PANEL_ROWS); bell.sync(); }
      if (page && pageList.dataset.page === "1") { prepend(pageList, d.entry, PAGE_ROWS); page.sync(); }
    });
    es.addEventListener("read", (m) => {
      const d = parse(m.data);
      if (!d) return;
      bell.setUnread(d.unread);
      const sel = d.all ? ".pb-notif[data-unread]" : `.pb-notif[data-id="${Math.trunc(Number(d.id)) || 0}"]`;
      document.querySelectorAll(sel).forEach((x) => x.removeAttribute("data-unread"));
    });
    es.addEventListener("error", () => {
      if (!es || es.readyState !== EventSource.CLOSED) return;
      es = null;
      clearTimeout(timer);
      timer = setTimeout(connect, backoff);
      backoff = Math.min(backoff * 2, 300_000);
    });
  }
  addEventListener("pagehide", disconnect);
  addEventListener("pageshow", (e) => { if (e.persisted) connect(); });
  document.addEventListener("visibilitychange", () => {
    clearTimeout(idle);
    if (document.visibilityState === "hidden") {
      idle = setTimeout(disconnect, 60_000);
    } else if (!es) {
      connect(); // its first event is the count
      if (panel.matches(":popover-open")) load();
    }
  });
  connect();
}

if (globalThis.document) {
  const bellWrap = document.querySelector('[data-inbox="bell"]');
  if (bellWrap) start(bellWrap);
}
