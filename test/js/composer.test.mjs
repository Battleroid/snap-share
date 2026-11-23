// Tests the pure parts of the composer: the Paste tab's paste and gist
// requests (js/composer.js) and the upload batches behind the album prompt
// (js/upload.js).
import { test } from "node:test";
import assert from "node:assert/strict";

import * as composer from "../../internal/web/static/js/composer.js";
import * as upload from "../../internal/web/static/js/upload.js";

// ---- Paste tab ----

test("one block is a paste with today's body", () => {
  assert.deepEqual(composer.pasteRequest("ignored", [{ name: "main.go", lang: "", content: "package main\n" }]), {
    url: "/api/v1/pastes",
    body: { content: "package main\n", name: "main.go" },
    done: "Paste created",
  });
  assert.deepEqual(composer.pasteBody({ name: "", lang: "go", content: "x" }), { content: "x", lang: "go" });
  assert.deepEqual(composer.pasteBody({ name: "", lang: "", content: "" }), { content: "" });
});

test("two or more blocks are a gist with every file in order", () => {
  const files = [
    { name: "main.go", lang: "", content: "package main\n" },
    { name: "", lang: "python", content: "print(1)\n" },
    { name: "README.md", lang: "markdown", content: "# hi\n" },
  ];
  const r = composer.pasteRequest("  Demo  ", files);
  assert.equal(r.url, "/api/v1/gists");
  assert.equal(r.done, "Gist created");
  assert.deepEqual(r.body, {
    title: "Demo",
    files: [
      { name: "main.go", lang: "", content: "package main\n" },
      { name: "", lang: "python", content: "print(1)\n" },
      { name: "README.md", lang: "markdown", content: "# hi\n" },
    ],
  });
  // A blank title is left out; missing fields are sent empty.
  assert.deepEqual(composer.gistBody(" ", [{ content: "a" }, { content: "b" }]), {
    files: [{ name: "", lang: "", content: "a" }, { name: "", lang: "", content: "b" }],
  });
});

test("the Paste tab's state follows the block count", () => {
  assert.deepEqual(composer.snippetState(1), { gist: false, label: "Create paste", canAdd: true });
  assert.deepEqual(composer.snippetState(2), { gist: true, label: "Create gist", canAdd: true });
  assert.equal(composer.MAX_GIST_FILES, 50);
  assert.equal(composer.snippetState(49).canAdd, true);
  assert.equal(composer.snippetState(50).canAdd, false);
});

// ---- upload batches ----

test("a batch completes when every file has settled, in any order", () => {
  const b = upload.newBatch(3);
  assert.equal(b.done, false);
  assert.equal(b.settle(2, { id: 30 }), false);
  assert.equal(b.settle(0, { id: 10 }), false);
  assert.equal(b.done, false);
  assert.equal(b.settle(1, { id: 20 }), true);
  assert.equal(b.done, true);
  // Upload order, not completion order.
  assert.deepEqual(b.items().map((i) => i.id), [10, 20, 30]);
});

test("failed files complete the batch but are left out", () => {
  const b = upload.newBatch(3);
  assert.equal(b.settle(1, null), false);
  assert.equal(b.settle(0, { id: 1 }), false);
  assert.equal(b.settle(2, { id: 3 }), true);
  assert.deepEqual(b.items().map((i) => i.id), [1, 3]);

  const failed = upload.newBatch(2);
  failed.settle(0, null);
  assert.equal(failed.settle(1, null), true);
  assert.deepEqual(failed.items(), []);
});

test("a file settles once and only files of the batch count", () => {
  const b = upload.newBatch(2);
  assert.equal(b.settle(0, { id: 1 }), false);
  assert.equal(b.settle(0, { id: 1 }), false);
  assert.equal(b.settle(0, null), false);
  assert.equal(b.settle(5, { id: 9 }), false);
  assert.equal(b.settle(-1, { id: 9 }), false);
  assert.equal(b.done, false);
  assert.equal(b.settle(1, { id: 2 }), true);
  assert.equal(b.settle(1, { id: 2 }), false);
  assert.deepEqual(b.items().map((i) => i.id), [1, 2]);
});

test("the album body carries the options, the title and the ids in order", () => {
  const options = { expires: "7d", visibility: "private", password: "correct horse", naming: "words" };
  assert.deepEqual(upload.albumBody(" Trip ", [{ id: 4 }, { id: 2 }], options), {
    expires: "7d", visibility: "private", password: "correct horse", naming: "words", title: "Trip", items: [4, 2],
  });
  assert.deepEqual(upload.albumBody("", [{ id: 1 }, { id: 2 }]), { items: [1, 2] });
});

// ---- options summary and the upload queue's heading ----

test("the options summary reads like the composer's settings and never shows the password", () => {
  assert.equal(composer.optionsSummary({ expires: "7d", visibility: "unlisted", naming: "random" }), "expires 7d · unlisted · random name · no password");
  assert.equal(
    composer.optionsSummary({ expires: "never", visibility: "private", naming: "original", password: "hunter2" }),
    "never expires · private · original name · password set",
  );
  assert.equal(composer.optionsSummary({ naming: "words" }), "never expires · unlisted · word name · no password");
});

test("the queue heading counts the files still going, then the failures", () => {
  assert.deepEqual(upload.queueTitle({ waiting: 2, failed: 0, sent: 1000, total: 5000, perSec: 0 }), { title: "Uploading 2 files", small: "1.0 KB of 5.0 KB" });
  assert.equal(upload.queueTitle({ waiting: 1, failed: 0, sent: 0, total: 10, perSec: 2000 }).small, "0 B of 10 B · 2.0 KB/s");
  assert.deepEqual(upload.queueTitle({ waiting: 0, failed: 1, sent: 0, total: 0, perSec: 0 }), { title: "1 upload failed", small: "" });
  assert.equal(upload.queueTitle({ waiting: 0, failed: 0, sent: 0, total: 0, perSec: 0 }).title, "");
});
