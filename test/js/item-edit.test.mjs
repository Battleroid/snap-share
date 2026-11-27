// The edit drawer's "keep (…)" expiry choice reads in words.
import { test } from "node:test";
import assert from "node:assert/strict";

import { keepLabel, relativeTime, splitTags, stemLength } from "../../internal/web/static/js/item-edit.js";

const now = Date.parse("2026-11-24T12:00:00Z");

test("the kept expiry is relative, never a hand-formatted timestamp", () => {
  assert.equal(keepLabel(null, now, "en"), "keep (never)");
  assert.equal(keepLabel("2026-11-27T12:00:00Z", now, "en"), "keep (in 3 days)");
  assert.equal(keepLabel("2026-11-24T14:00:00Z", now, "en"), "keep (in 2 hours)");
  assert.equal(keepLabel("2026-11-23T12:00:00Z", now, "en"), "keep (yesterday)");
});

test("relative times pick the largest whole unit", () => {
  assert.equal(relativeTime("2026-11-24T12:30:00Z", now, "en"), "in 30 minutes");
  assert.equal(relativeTime("2026-12-08T12:00:00Z", now, "en"), "in 2 weeks");
  assert.equal(relativeTime("2026-11-24T12:00:20Z", now, "en"), "in 20 seconds");
});

test("tags split on newlines, commas and semicolons", () => {
  assert.deepEqual(splitTags("a, b;c"+String.fromCharCode(10)+" d ,,"), ["a", "b", "c", "d"]);
});

test("Rename selects the name before the extension", () => {
  assert.equal(stemLength("bundle.tar.gz"), "bundle.tar".length);
  assert.equal(stemLength("notes"), 5);
  assert.equal(stemLength(".env"), 4);
});
