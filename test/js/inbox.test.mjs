// Tests the parts of js/inbox.js that take server data: the link guard and
// the row built from a stream event (textContent only, known icons and
// statuses).
import { test } from "node:test";
import assert from "node:assert/strict";

import { rowElement, safeHref } from "../../internal/web/static/js/inbox.js";

// A document with just what rowElement uses.
class El {
  constructor(tag) {
    this.tagName = tag.toUpperCase();
    this.className = "";
    this.dataset = {};
    this.attrs = {};
    this.children = [];
    this.textContent = "";
  }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  append(...c) { this.children.push(...c); }
}
const doc = { createElement: (t) => new El(t) };

test("only paths on this origin are followed", () => {
  for (const ok of ["/dashboard/items/7/stats", "/dashboard/secrets/QUJDREVGR0hJSktMTU5PUA/timeline", "/"]) {
    assert.equal(safeHref(ok), ok);
  }
  for (const bad of ["//evil.example/x", "/\\evil.example", "https://evil.example/", "javascript:alert(1)", "dashboard", "", undefined, null, 7]) {
    assert.equal(safeHref(bad), "", String(bad));
  }
});

test("a stream entry becomes a row without parsing HTML", () => {
  const li = rowElement(doc, {
    id: 12, status: "down", icon: "status-down", unread: true, link: "/dashboard/secrets/QUJDREVGR0hJSktMTU5PUA/timeline",
    title: '<img src=x onerror="alert(1)"> burned', body: "last attempt <b>x</b>", time: "now", at: "2026-11-22T14:03:00Z",
  });
  assert.equal(li.tagName, "LI");
  assert.equal(li.className, "pb-notif");
  assert.deepEqual(li.dataset, { id: "12", status: "down", href: "/dashboard/secrets/QUJDREVGR0hJSktMTU5PUA/timeline" });
  assert.equal(li.attrs["data-unread"], "");
  assert.equal(li.attrs.tabindex, "0");
  const [icon, title, time, body] = li.children;
  assert.equal(icon.className, "pb-icon pb-icon--status-down");
  assert.equal(title.className, "pb-notif__title");
  assert.equal(title.textContent, '<img src=x onerror="alert(1)"> burned');
  assert.equal(time.tagName, "TIME");
  assert.equal(time.textContent, "now");
  assert.equal(time.attrs.datetime, "2026-11-22T14:03:00Z");
  assert.equal(body.textContent, "last attempt <b>x</b>");
});

test("unknown icons, statuses, ids and links fall back", () => {
  const li = rowElement(doc, { id: "1 or 1=1", status: "evil", icon: 'eye" onload="x', link: "//evil.example", title: "t" });
  assert.equal(li.dataset.id, "0");
  assert.equal(li.dataset.status, "info");
  assert.equal(li.dataset.href, "");
  assert.equal(li.attrs["data-unread"], undefined);
  assert.equal(li.children[0].className, "pb-icon pb-icon--bell");
  assert.equal(li.children.length, 3); // no body
});
