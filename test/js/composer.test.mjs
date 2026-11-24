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

// ---- the album prompt's file set ----

test("two uploaded files make the set offer an album, in picked order", () => {
  const set = upload.albumSet();
  assert.equal(set.add({ id: 30 }, 3), false);
  assert.equal(set.add({ id: 10 }, 1), true);
  assert.equal(set.add({ id: 20 }, 2), true);
  assert.deepEqual(set.items().map((i) => i.id), [10, 20, 30]);
  assert.equal(set.count, 3);
});

test("a file counts once, and clearing or removing forgets files", () => {
  const set = upload.albumSet();
  set.add({ id: 1 }, 1);
  assert.equal(set.add({ id: 1 }, 1), false);
  set.add({ id: 2 }, 2);
  set.add({ id: 3 }, 3);
  set.remove([{ id: 1 }, { id: 2 }]);
  assert.deepEqual(set.items().map((i) => i.id), [3]);
  set.clear();
  assert.equal(set.count, 0);
  assert.equal(set.add(null), false);
});

test("the prompt counts the files", () => {
  assert.equal(upload.offerTitle(3), "3 files uploaded");
});

test("the album body carries the options, the title and the ids in order", () => {
  const options = { expires: "7d", visibility: "private", password: "correct horse", naming: "words" };
  assert.deepEqual(upload.albumBody(" Trip ", [{ id: 4 }, { id: 2 }], options), {
    expires: "7d", visibility: "private", password: "correct horse", naming: "words", title: "Trip", items: [4, 2],
  });
  assert.deepEqual(upload.albumBody("", [{ id: 1 }, { id: 2 }]), { items: [1, 2] });
});

// ---- Save as default ----

test("save as default posts the composer's options over the rest of the defaults, and no password", () => {
  const body = composer.defaultsBody(
    { expires: "7d", visibility: "private", naming: "words", password: "hunter2" },
    [["random_length", "12"], ["strip_location", "1"], ["embed_title", ""]],
  );
  assert.equal(body.toString(), "random_length=12&strip_location=1&embed_title=&naming=words&visibility=private&expiry=7d");
  assert.equal(composer.defaultsBody({}, []).toString(), "naming=random&visibility=unlisted&expiry=never");
});

// ---- field errors ----

test("a link error belongs to the field it is about", () => {
  assert.equal(composer.fieldForError("link", "That name is taken."), "slug");
  assert.equal(composer.fieldForError("link", "That name is reserved."), "slug");
  assert.equal(composer.fieldForError("link", "Use 1-64 letters, digits, - and _ for the name."), "slug");
  assert.equal(composer.fieldForError("link", "Enter a URL."), "url");
  assert.equal(composer.fieldForError("link", "Links must be http or https URLs."), "url");
  assert.equal(composer.fieldForError("link", "Links with a user name or password are not allowed."), "url");
  assert.equal(composer.fieldForError("link", "Request failed (500)."), "");
});

test("only an empty paste belongs to a field", () => {
  assert.equal(composer.fieldForError("paste", "The paste is empty."), "content");
  assert.equal(composer.fieldForError("paste", "The gist was not created."), "");
  assert.equal(composer.fieldForError("paste", "That name is taken."), "");
});
