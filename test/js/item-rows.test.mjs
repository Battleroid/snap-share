// The type word beside a row's size comes from the server (list_type).
import { test } from "node:test";
import assert from "node:assert/strict";

import { TYPE_WORDS, typeWord, processing } from "../../internal/web/static/js/item-rows.js";

test("a row shows the type word the server gave it", () => {
  for (const w of TYPE_WORDS) assert.equal(typeWord({ list_type: w, type: "image/png", name: "a.png" }), w);
});

test("an unknown or missing word is other, never an extension", () => {
  assert.equal(typeWord({ type: "image/png", name: "a.png" }), "other");
  assert.equal(typeWord({ list_type: "png" }), "other");
});

// A document with just what processing() uses.
class El {
  constructor(tag) {
    this.tagName = tag.toUpperCase();
    this.className = "";
    this.attrs = {};
    this.children = [];
    this.textContent = "";
  }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  append(...c) { this.children.push(...c); }
}

test("the processing marker polls its own item's fragment every 5 s", () => {
  globalThis.document = { createElement: (t) => new El(t) };
  try {
    const m = processing({ id: 42 });
    assert.equal(m.attrs["hx-get"], "/dashboard/items/42/media?as=lead");
    assert.equal(m.attrs["hx-trigger"], "every 5s");
    assert.equal(m.attrs["hx-swap"], "outerHTML");
    assert.equal(m.attrs.role, "status");
    assert.deepEqual(m.children.map((c) => c.textContent), ["", "processing"]);
    // Nothing that runs script: no inline handlers, no hx-on.
    assert.deepEqual(Object.keys(m.attrs).filter((k) => /^(on|hx-on|data-hx-on)/.test(k)), []);
  } finally {
    delete globalThis.document;
  }
});
