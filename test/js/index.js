// Entry point for `node --test test/js/` (make test-js). Node 24 does not
// expand a directory argument; it loads the directory as a module, which
// resolves to this file. It imports every *.test.mjs here in name order.
// Not needed once the Makefile passes a glob: node --test 'test/js/**/*.test.mjs'.
"use strict";
const { readdirSync } = require("node:fs");
const { join } = require("node:path");
const { pathToFileURL } = require("node:url");

const files = readdirSync(__dirname)
  .filter((f) => f.endsWith(".test.mjs"))
  .sort();

(async () => {
  for (const f of files) await import(pathToFileURL(join(__dirname, f)).href);
})().catch((err) => {
  console.error(err);
  process.exit(1);
});
