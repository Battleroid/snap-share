// Tests the part of js/viewers.js that takes page data: the viewers filter
// hides events and empty day headings. (data-vars is the kit's vars hook.)
import { test } from "node:test";
import assert from "node:assert/strict";

import { chosen, filterViewers } from "../../internal/web/static/js/viewers.js";

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
