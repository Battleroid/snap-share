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

.PHONY: help dev-image go test test-go test-js lint vuln fmt tidy build image clean demo-up demo-down demo-logs e2e e2e-isolated e2e-isolated-media shots

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
	@if [ -d test/js ]; then $(DOCKER_RUN) $(NODE_IMAGE) node --test 'test/js/**/*.test.mjs'; else echo "no js tests"; fi

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
	docker build -q --build-arg VERSION=$(shell git describe --always --dirty 2>/dev/null || echo dev) --build-arg COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo unknown) -t $(IMAGE) . >/dev/null

# DEMO_HOST is the address the demo binds to (loopback by default; set your LAN
# address to try it from another machine); DEMO_NAME is its public name
# (a hostname: browsers refuse passkeys on IP addresses). docs/dev/demo.md.
DEMO_HOST ?= 127.0.0.1
DEMO_NAME ?= $(subst .,-,$(DEMO_HOST)).sslip.io
DEMO_ENV  := DEMO_HOST=$(DEMO_HOST) DEMO_NAME=$(DEMO_NAME) DEMO_UID=$(shell id -u) DEMO_GID=$(shell id -g) DEMO_IMAGE=$(IMAGE)
DEMO      := $(DEMO_ENV) docker compose -f deploy/demo/compose.yml

demo-up: image ## run the local demo at https://$(DEMO_NAME):8443 (mail at http://$(DEMO_HOST):8025)
	@mkdir -p .demo/data .demo/caddy/data .demo/caddy/config
	@test -f .demo/snap.env || { umask 077; printf 'SNAP_SECRET_KEY=%s\n' "$$(head -c 48 /dev/urandom | base64 -w0)" > .demo/snap.env; }
	$(DEMO) up -d --wait --force-recreate
	@echo "demo: https://$(DEMO_NAME):8443  mail: http://$(DEMO_HOST):8025"

demo-down: ## stop the local demo (keeps .demo/ state)
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

e2e: test/e2e/node_modules ## Playwright against the running local demo
	$(PW_RUN) --network host --add-host $(DEMO_NAME):$(DEMO_HOST) -e BASE_URL=https://$(DEMO_NAME):8443 $(PW_IMAGE) npx playwright test $(ARGS)

shots: test/e2e/node_modules ## screenshots of PAGES="/ /login" from the local demo into test/e2e/out/shots
	$(PW_RUN) --network host --add-host $(DEMO_NAME):$(DEMO_HOST) -e BASE_URL=https://$(DEMO_NAME):8443 -e COOKIE $(PW_IMAGE) node shots.mjs $(or $(PAGES),/)

review: test/e2e/node_modules ## sign in as the demo review user; screenshot signed-in pages into test/e2e/out/review
	$(PW_RUN) --network host --add-host $(DEMO_NAME):$(DEMO_HOST) -e BASE_URL=https://$(DEMO_NAME):8443 -e E2E_USER=review -e E2E_PASSWORD="$$(cat .demo/review.pw)" $(PW_IMAGE) node review.mjs $(PAGES)

E2E_PROJECT := snap-e2e-$(shell echo $(CURDIR) | sha256sum | cut -c1-8)
# E2E_COMPOSE_EXTRA adds compose overrides, e.g. deploy/e2e/compose.media.yml (e2e-isolated-media).
E2E_COMPOSE_EXTRA ?=
E2E         := E2E_IMAGE=snap:$(E2E_PROJECT) E2E_UID=$(shell id -u) E2E_GID=$(shell id -g) docker compose -p $(E2E_PROJECT) -f deploy/e2e/compose.yml $(foreach f,$(E2E_COMPOSE_EXTRA),-f $(f))

e2e-isolated: test/e2e/node_modules ## Playwright against a private stack (no published ports; safe in parallel worktrees)
	$(MAKE) image IMAGE=snap:$(E2E_PROJECT)
	$(E2E) up -d --wait
	printf '%s\n' 'e2e-admin-password' | $(E2E) exec -T snap snap-server admin create-user --username admin --email admin@e2e.invalid --admin --password-stdin || { $(E2E) down -v; exit 1; }
	printf '%s\n' 'e2e-media-password' | $(E2E) exec -T snap snap-server admin create-user --username media --email media@e2e.invalid --password-stdin || { $(E2E) down -v; exit 1; }
	$(PW_RUN) --network $(E2E_PROJECT)_default -e BASE_URL=https://snap-e2e.test:8443 -e E2E_USER=admin -e E2E_PASSWORD=e2e-admin-password -e E2E_MEDIA_USER=media -e E2E_MEDIA_PASSWORD=e2e-media-password $(PW_IMAGE) npx playwright test $(ARGS); rc=$$?; \
	if [ $$rc -eq 0 ] && $(E2E) ps --services | grep -qx media; then \
	  $(E2E) logs --no-log-prefix media | grep '"op":"poster"' | grep '"request_id":"job-' | grep -q '"result":"ok"' \
	    || { echo 'e2e: the media worker made no poster'; rc=1; }; \
	fi; \
	$(E2E) down -v; exit $$rc

e2e-isolated-media: ## e2e-isolated with ffmpeg in a media worker container (deploy/e2e/compose.media.yml)
	$(MAKE) e2e-isolated E2E_COMPOSE_EXTRA=deploy/e2e/compose.media.yml ARGS="$(ARGS)"
