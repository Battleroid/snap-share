// Tests the parts of js/vars.js and js/viewers.js that take page data: only
// known custom properties with plain values are applied, and the viewers
// filter hides events and empty day headings.
import { test } from "node:test";
import assert from "node:assert/strict";

import { parseVars } from "../../internal/web/static/js/vars.js";
import { chosen, filterViewers } from "../../internal/web/static/js/viewers.js";

test("data-vars applies known properties with plain values", () => {
  assert.deepEqual(parseVars("--w:12.5%;--c:var(--chart-1)"), [["--w", "12.5%"], ["--c", "var(--chart-1)"]]);
  assert.deepEqual(parseVars(" --value : 82% ; --span:6 "), [["--value", "82%"], ["--span", "6"]]);
  assert.deepEqual(parseVars(undefined), []);
});

test("data-vars refuses what is not a kit custom property or a plain value", () => {
  for (const bad of [
    "--evil:1", "color:red", "--w:url(https://evil.example/x)", "--w:12px", "--c:red", "--c:var(--a);--w:expression(1)",
    "--w:calc(1+1)", "--c:var(--x, red)", "background:var(--c)", "--w", "",
  ]) {
    const got = parseVars(bad);
    assert.ok(got.every(([n, v]) => ["--w", "--c", "--value", "--span", "--p", "--at"].includes(n) && /^(?:\d+(?:\.\d+)?%?|var\(--[a-z0-9-]+\))$/.test(v)), bad);
  }
  assert.deepEqual(parseVars("--c:var(--a);--w:expression(1)"), [["--c", "var(--a)"]]);
});

// A fake DOM: a strip with chips, and a timeline of day headings and events.
class Node_ {
  constructor(cls, data = {}) {
    this.cls = cls;
    this.dataset = data;
    this.hidden = false;
    this.attrs = {};
  }
  matches(sel) { return sel === ".pb-timeline__day" && this.cls === "day"; }
  setAttribute(k, v) { this.attrs[k] = v; }
  getAttribute(k) { return this.attrs[k] ?? null; }
}

function timeline() {
  const chips = [new Node_("chip", { viewer: "all" }), new Node_("chip", { viewer: "v1" }), new Node_("chip", { viewer: "v2" })];
  chips[0].attrs["aria-pressed"] = "true";
  const rows = [
    new Node_("day"), new Node_("ev", { viewer: "v1" }), new Node_("ev", { viewer: "v1" }),
    new Node_("day"), new Node_("ev", { viewer: "v2" }), new Node_("ev", { viewer: "v1" }),
    new Node_("day"), new Node_("ev", { viewer: "v2" }),
  ];
  const strip = { querySelectorAll: () => chips, parentElement: { querySelectorAll: () => rows, querySelector: () => null } };
  return { strip, chips, rows };
}

test("a viewer chip shows only that viewer, and a day with none is hidden", () => {
  const { strip, chips, rows } = timeline();
  filterViewers(strip, "v1");
  assert.deepEqual(rows.map((r) => r.hidden), [false, false, false, false, true, false, true, true]);
  assert.deepEqual(chips.map((c) => c.attrs["aria-pressed"]), ["false", "true", "false"]);
  filterViewers(strip, "all");
  assert.ok(rows.every((r) => !r.hidden));
  assert.deepEqual(chips.map((c) => c.attrs["aria-pressed"]), ["true", "false", "false"]);
});

test("choosing the pressed chip again goes back to everyone", () => {
  const { chips } = timeline();
  assert.equal(chosen(chips[1]), "v1");
  chips[1].attrs["aria-pressed"] = "true";
  assert.equal(chosen(chips[1]), "all");
});

test("when a viewer has no events on this page, a note says so", () => {
  const { strip, rows } = timeline();
  const note = new Node_("note");
  note.hidden = true;
  strip.parentElement.querySelector = (sel) => (sel === "[data-viewers-empty]" ? note : null);
  filterViewers(strip, "v1");
  assert.equal(note.hidden, true);
  for (const r of rows) if (r.dataset.viewer === "v1") r.dataset.viewer = "v9"; // none left for v1
  filterViewers(strip, "v1");
  assert.equal(note.hidden, false);
  filterViewers(strip, "all");
  assert.equal(note.hidden, true);
});
