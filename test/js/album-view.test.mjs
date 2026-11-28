// The album switch keeps a visitor's view in the URL, and only when it is
// not the album's default.
import { test } from "node:test";
import assert from "node:assert/strict";

import { viewURL } from "../../internal/web/static/js/album-view.js";

test("a view other than the default goes in the URL", () => {
  assert.equal(viewURL("https://snap.test/abc", "list", "grid"), "/abc?view=list");
  assert.equal(viewURL("https://snap.test/abc?view=grid#x", "list", "grid"), "/abc?view=list#x");
});

test("the default leaves the URL clean", () => {
  assert.equal(viewURL("https://snap.test/abc?view=list", "grid", "grid"), "/abc");
  assert.equal(viewURL("https://snap.test/abc?view=grid", "list", "list"), "/abc");
});
