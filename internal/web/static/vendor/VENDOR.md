# Vendored browser files

| Path | Package | Version | Source | Integrity |
|---|---|---|---|---|
| `../patchbay/`, `../js/patchbay.js` | Patchbay design kit (soft icons only); `patchbay.js` ports its reference scripts | 2 | project design kit | — |
| `htmx/htmx.min.js`, `htmx/LICENSE` | htmx.org | 2.0.11 | https://registry.npmjs.org/htmx.org/-/htmx.org-2.0.11.tgz | sha512-Thx/WtpeOQqSrqBCw/A1cwGJGg4UrVa3+sW0GmrM3p4gJgO89ecH4qtbnyzDDWFvBTqjnIMCgELTNt636dtamA== |
| `hash-wasm/` | hash-wasm: `dist/argon2.umd.min.js` and `LICENSE` only (Argon2id for E2E passphrases; MIT) | 4.13.0 | https://registry.npmjs.org/hash-wasm/-/hash-wasm-4.13.0.tgz | tarball `sha512-GrWl7wsV46fKIXQZB5ebskLZLAmKL6d9RS0fspCxDeQyjoNu1AfTJTMLPOJyfzaQm0ldFiwFN21/w+sE/kgEFQ==` (npm `dist.integrity`); `argon2.umd.min.js` sha256 `acfe11b015852f593aaddc51116f71dc7850e4551e6b34d42c1f88cf39635f03` |
| `../js/eff-wordlist.js` | EFF large wordlist for passphrases (7776 words; dice numbers dropped, words unchanged). © Electronic Frontier Foundation, CC BY 3.0 US | 2016-07-18 | https://www.eff.org/files/2016/07/18/eff_large_wordlist.txt | source file sha256 `addd35536511597a02fa0a9ff1e5284677b8883b83e986e43f15a3db996b903e` (checked by test/js/secrets.test.mjs) |
