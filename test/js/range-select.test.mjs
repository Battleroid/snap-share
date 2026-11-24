// Shift-click range selection: which boxes a range covers.
import { test } from "node:test";
import assert from "node:assert/strict";

import { between } from "../../internal/web/static/js/range-select.js";

const boxes = ["a", "b", "c", "d", "e"];

test("a range covers both ends and everything between, either way round", () => {
  assert.deepEqual(between(boxes, "b", "d"), ["b", "c", "d"]);
  assert.deepEqual(between(boxes, "d", "b"), ["b", "c", "d"]);
  assert.deepEqual(between(boxes, "c", "c"), ["c"]);
});

test("an anchor from a list that was swapped out gives no range", () => {
  assert.deepEqual(between(boxes, "z", "b"), []);
  assert.deepEqual(between(boxes, null, "b"), []);
});
