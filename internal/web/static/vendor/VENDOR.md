# Vendored browser files

| Path | Package | Version | Source | Integrity |
|---|---|---|---|---|
| `patchbay/` | Patchbay design kit: `patchbay.css`, `patchbay.mjs` (ES module; `pbInit` and the kit behaviours, loaded by `js/app.js`), `pb-theme-boot.js` (blocking theme boot in `<head>`), `tokens.json`, `fonts/` (SIL OFL 1.1, `fonts/OFL.txt`), `LICENSE` (MIT). Icons are data URIs inside the CSS. | branch `ts-date-format`, commit `fc0a436cabc64325f5ffdeafe2c02801d5d4f2ae` (the head of the open kit PR that adds the time-series `xFormat: "date"`, on top of `main` 0aa397c). Re-pin to the kit's `main` after that PR is merged: re-run the `git archive` below from the merge commit, then update this row, the hashes and the docs under `docs/dev/patchbay` | https://github.com/Battleroid/patchbay-ui-kit | per file below, checked by `TestVendoredPatchbayHashes` |
| `htmx/htmx.min.js`, `htmx/LICENSE` | htmx.org | 2.0.11 | https://registry.npmjs.org/htmx.org/-/htmx.org-2.0.11.tgz | sha512-Thx/WtpeOQqSrqBCw/A1cwGJGg4UrVa3+sW0GmrM3p4gJgO89ecH4qtbnyzDDWFvBTqjnIMCgELTNt636dtamA== |
| `hash-wasm/` | hash-wasm: `dist/argon2.umd.min.js` and `LICENSE` only (Argon2id for E2E passphrases; MIT) | 4.13.0 | https://registry.npmjs.org/hash-wasm/-/hash-wasm-4.13.0.tgz | tarball `sha512-GrWl7wsV46fKIXQZB5ebskLZLAmKL6d9RS0fspCxDeQyjoNu1AfTJTMLPOJyfzaQm0ldFiwFN21/w+sE/kgEFQ==` (npm `dist.integrity`); `argon2.umd.min.js` sha256 `acfe11b015852f593aaddc51116f71dc7850e4551e6b34d42c1f88cf39635f03` |
| `../js/eff-wordlist.js` | EFF large wordlist for passphrases (7776 words; dice numbers dropped, words unchanged). © Electronic Frontier Foundation, CC BY 3.0 US | 2016-07-18 | https://www.eff.org/files/2016/07/18/eff_large_wordlist.txt | source file sha256 `addd35536511597a02fa0a9ff1e5284677b8883b83e986e43f15a3db996b903e` (checked by test/js/secrets.test.mjs) |

## Patchbay files

Copied byte for byte from the commit above (`git archive fc0a436 patchbay.css patchbay.mjs pb-theme-boot.js tokens.json LICENSE fonts`). Update a file and its line here together.

| File | sha256 |
|---|---|
| `patchbay/patchbay.css` | `49ba006bb4d3df1a5a6adde4142c50aedce897d8ec145fdd19ea48c96acb5725` |
| `patchbay/patchbay.mjs` | `7667d5c9e9d9b0858ce1042b00cbe4255d237a634394c740e518a70349bed9c9` |
| `patchbay/pb-theme-boot.js` | `83521b2a6af88ff92fcefc07296804f24ba34a9ee767708e88697b30de8b5f9e` |
| `patchbay/tokens.json` | `11caecf912b37c297243adc5cd74cb56fb61836a3396c5e127e116d08cc596aa` |
| `patchbay/LICENSE` | `82cf35057df8e0d5cf86be124cd2db163ca4613a5b1a4c6cbf603e2d5b6add3b` |
| `patchbay/fonts/OFL.txt` | `55d463b463e4673f2255163d4d123055285dc0209af3564185f18dcbe0316e5e` |
| `patchbay/fonts/ObelMono-Italic-VF.woff2` | `a59638d917e31e3a7a1a5ea07937142911a7bdb8900fd86a78ab956a16212476` |
| `patchbay/fonts/ObelMono-VF.woff2` | `d1fd3dfb55420d2d39414ea4a9757e427718910009131527eedf911ae702fb0f` |
| `patchbay/fonts/SourceSerif4-Italic-Variable.woff2` | `96e6393209728ab93c334ae739af8a040cd2006c82e8614d6465a8084db88f89` |
| `patchbay/fonts/SourceSerif4-Variable.woff2` | `f2ea9c12d2fe9bd3a9589b02ad2c0909da88f30938c91adc838c4f4098f9f9e0` |
