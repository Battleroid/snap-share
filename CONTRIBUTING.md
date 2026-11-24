# Contributing

Thanks for looking. snap is a small project; bug reports and focused pull requests are welcome.

## Before you start

- For anything larger than a fix, open an issue first so we can agree on the shape of the change.
- Security problems: please do not file a public issue. Report them privately through the repository's Security tab (Report a vulnerability).
- [docs/dev/conventions.md](docs/dev/conventions.md) has the rules for code, UI, docs and commits. [CLAUDE.md](CLAUDE.md) is the same orientation in one page, written for coding assistants but useful to anyone: the architecture and the tests that guard more than code.

## Setup

Docker (with the compose plugin) and `make` are all you need. Every tool runs in a pinned container, and the module and build caches live in `~/.cache/snap`.

```sh
make test      # go vet + go test -race, then the browser module tests
make lint      # golangci-lint
make vuln      # govulncheck
```

`make help` lists every target. `make e2e-isolated` runs the Playwright suite against a private stack with no published ports, and `make demo-up` starts a [local demo](docs/dev/demo.md).

## Making a change

1. Branch from `main`.
2. Keep the change focused, with tests; a bug fix comes with a test that fails without it. For security findings, add a regression test to `internal/redteam` ([CLAUDE.md](CLAUDE.md#tests-that-guard-more-than-code)).
3. Update the docs in the same change. `internal/app/docs_test.go` fails when `docs/configuration.md` misses a variable or setting, a relative link breaks, or a page isn't listed in `docs/README.md`.
4. Run `make test`, `make lint`, `make vuln` and `make e2e-isolated`. CI runs the same targets.
5. Commit with messages of the form `area: what changed`, for example `items: reject an empty slug`. Small commits are easier to review.
6. If the change is visible in the UI, include before and after screenshots in the pull request, taken at the same size.

Tests must not use the network or publish Docker ports.

## Vendored files

The Patchbay kit, htmx and hash-wasm in `internal/web/static/vendor/` are copied byte for byte and pinned by hash in `internal/web/static/vendor/VENDOR.md`. Don't edit them by hand; update them as described there.
