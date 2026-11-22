# Toolchains run in containers; nothing is installed on the host.
DEV_IMAGE  := snap-dev:$(shell sha256sum docker/dev.Dockerfile | cut -c1-12)
NODE_IMAGE := node:24.22.3-alpine3.24
CACHE      ?= $(HOME)/.cache/snap
TAG        ?= dev
IMAGE      ?= snap:$(TAG)
PKG        ?= ./...
ARGS       ?=

DOCKER_RUN := docker run --rm -u $(shell id -u):$(shell id -g) \
	-e HOME=/tmp -e GOCACHE=/cache/go-build -e GOMODCACHE=/cache/go-mod \
	-e GOLANGCI_LINT_CACHE=/cache/golangci -e GOFLAGS=-buildvcs=false \
	-v $(CACHE):/cache -v $(CURDIR):/src -w /src

.PHONY: help dev-image go test test-go test-js lint vuln fmt tidy build image clean demo-up demo-down demo-logs e2e e2e-isolated shots

help: ## list targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "%-12s %s\n", $$1, $$2}'

$(CACHE):
	mkdir -p $(CACHE)/go-build $(CACHE)/go-mod $(CACHE)/golangci

dev-image: | $(CACHE) ## build the toolchain image if missing
	@docker image inspect $(DEV_IMAGE) >/dev/null 2>&1 || docker build -q -t $(DEV_IMAGE) -f docker/dev.Dockerfile docker >/dev/null

go: dev-image ## run a go command, e.g. make go ARGS="mod tidy"
	$(DOCKER_RUN) $(DEV_IMAGE) go $(ARGS)

test: test-go test-js ## all tests

test-go: dev-image ## go vet + go test -race; narrow with PKG=./internal/e2e/...
	$(DOCKER_RUN) $(DEV_IMAGE) sh -c 'go vet $(PKG) && go test -race -count=1 $(PKG)'

test-js: | $(CACHE) ## node tests for browser modules (test/js)
	@if [ -d test/js ]; then $(DOCKER_RUN) $(NODE_IMAGE) node --test test/js/; else echo "no js tests"; fi

lint: dev-image ## golangci-lint
	$(DOCKER_RUN) $(DEV_IMAGE) golangci-lint run $(PKG)

vuln: dev-image ## govulncheck
	$(DOCKER_RUN) $(DEV_IMAGE) govulncheck $(PKG)

fmt: dev-image ## gofmt -s -w
	$(DOCKER_RUN) $(DEV_IMAGE) gofmt -s -w .

tidy: dev-image ## go mod tidy
	$(DOCKER_RUN) $(DEV_IMAGE) go mod tidy

build: dev-image ## build binaries into bin/
	$(DOCKER_RUN) -e CGO_ENABLED=0 $(DEV_IMAGE) go build -trimpath -o bin/ ./cmd/...

image: ## build the runtime image (IMAGE=snap:dev)
	docker build -q --build-arg VERSION=$(shell git describe --always --dirty 2>/dev/null || echo dev) -t $(IMAGE) . >/dev/null

DEMO_HOST ?= 203.0.113.5
DEMO_ENV  := DEMO_HOST=$(DEMO_HOST) DEMO_UID=$(shell id -u) DEMO_GID=$(shell id -g) DEMO_IMAGE=$(IMAGE)
DEMO      := $(DEMO_ENV) docker compose -f deploy/demo/compose.yml

demo-up: image ## run the LAN demo at https://$(DEMO_HOST):8443 (mail at :8025)
	@mkdir -p .demo/data .demo/caddy/data .demo/caddy/config
	@test -f .demo/snap.env || { umask 077; printf 'SNAP_SECRET_KEY=%s\n' "$$(head -c 48 /dev/urandom | base64 -w0)" > .demo/snap.env; }
	$(DEMO) up -d --wait --force-recreate
	@echo "demo: https://$(DEMO_HOST):8443  mail: http://$(DEMO_HOST):8025"

demo-down: ## stop the LAN demo (keeps .demo/ state)
	$(DEMO) down

demo-logs: ## follow the demo server log
	$(DEMO) logs -f snap

clean: ## remove build output
	rm -rf bin

PW_IMAGE := mcr.microsoft.com/playwright:v1.65.1-noble
PW_RUN   := docker run --rm --ipc=host -u $(shell id -u):$(shell id -g) -e HOME=/tmp -v $(CURDIR)/test/e2e:/e2e -w /e2e

test/e2e/node_modules: test/e2e/package-lock.json
	docker run --rm -u $(shell id -u):$(shell id -g) -e HOME=/tmp -e npm_config_cache=/cache/npm -v $(CACHE):/cache -v $(CURDIR)/test/e2e:/e2e -w /e2e $(NODE_IMAGE) npm ci --no-audit --no-fund --ignore-scripts
	@touch $@

e2e: test/e2e/node_modules ## Playwright against the running LAN demo
	$(PW_RUN) --network host -e BASE_URL=https://$(DEMO_HOST):8443 $(PW_IMAGE) npx playwright test $(ARGS)

shots: test/e2e/node_modules ## screenshots of PAGES="/ /login" from the LAN demo into test/e2e/out/shots
	$(PW_RUN) --network host -e BASE_URL=https://$(DEMO_HOST):8443 -e COOKIE $(PW_IMAGE) node shots.mjs $(or $(PAGES),/)

E2E_PROJECT := snap-e2e-$(shell echo $(CURDIR) | sha256sum | cut -c1-8)
E2E         := E2E_IMAGE=snap:$(E2E_PROJECT) E2E_UID=$(shell id -u) E2E_GID=$(shell id -g) docker compose -p $(E2E_PROJECT) -f deploy/e2e/compose.yml

e2e-isolated: test/e2e/node_modules ## Playwright against a private stack (no published ports; safe in parallel worktrees)
	$(MAKE) image IMAGE=snap:$(E2E_PROJECT)
	$(E2E) up -d --wait
	printf '%s\n' 'e2e-admin-password' | $(E2E) exec -T snap snap-server admin create-user --username admin --email admin@e2e.invalid --admin --password-stdin || { $(E2E) down -v; exit 1; }
	$(PW_RUN) --network $(E2E_PROJECT)_default -e BASE_URL=https://snap-e2e:8443 $(PW_IMAGE) npx playwright test $(ARGS); rc=$$?; $(E2E) down -v; exit $$rc
