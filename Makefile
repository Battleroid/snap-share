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

.PHONY: help dev-image go test test-go test-js lint vuln fmt tidy build image clean

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
	docker build -t $(IMAGE) .

clean: ## remove build output
	rm -rf bin
