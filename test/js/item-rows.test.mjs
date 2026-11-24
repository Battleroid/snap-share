// The type word beside a row's size comes from the server (list_type).
import { test } from "node:test";
import assert from "node:assert/strict";

import { TYPE_WORDS, typeWord, processingAttrs } from "../../internal/web/static/js/item-rows.js";

test("a row shows the type word the server gave it", () => {
  for (const w of TYPE_WORDS) assert.equal(typeWord({ list_type: w, type: "image/png", name: "a.png" }), w);
});

test("an unknown or missing word is other, never an extension", () => {
  assert.equal(typeWord({ type: "image/png", name: "a.png" }), "other");
  assert.equal(typeWord({ list_type: "png" }), "other");
});

test("the processing marker polls its own item's fragment every 5 s", () => {
  const a = processingAttrs({ id: 42 });
  assert.deepEqual(a, { "hx-get": "/dashboard/items/42/media?as=lead", "hx-trigger": "every 5s", "hx-swap": "outerHTML" });
  // Nothing that runs script: no inline handlers, no hx-on.
  assert.deepEqual(Object.keys(a).filter((k) => /^(on|hx-on|data-hx-on)/.test(k)), []);
});
