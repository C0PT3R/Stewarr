SHELL := /bin/sh

-include Makefile.local

REMOTE ?= user@your-server
REMOTE_DIR ?= ./servarr/stewarr

.PHONY: build run fmt test test-browser deploy logs assets typecheck

# typecheck runs tsc --strict over internal/httpui/static/src. This is the
# only place Node/npm is required at build time — esbuild (below) only
# transpiles TypeScript, it does not check it, so skipping this step would
# let type errors reach the bundle silently.
typecheck:
	npm --prefix internal/httpui/static/src ci --silent
	npm --prefix internal/httpui/static/src run --silent typecheck

# assets bundles internal/httpui/static/src/*.ts into the single
# internal/httpui/static/app.js file //go:embed serves, via esbuild's Go API
# (no Node/npm involved in the bundling itself, only in typecheck above).
assets: typecheck
	go run ./tools/buildassets

build: assets
	go build ./cmd/stewarr

run: assets
	go run ./cmd/stewarr -config ./config.json

fmt:
	gofmt -w ./cmd ./internal ./tools

test:
	go test ./...

test-browser:
	node tests/ui/reactivity.mjs

deploy: assets
	@stage=$$(mktemp -d); \
	trap 'rm -rf "$$stage"' EXIT HUP INT TERM; \
	tar -cf - -T .release-manifest | tar -xf - -C "$$stage"; \
	ssh $(REMOTE) 'mkdir -p $(REMOTE_DIR)/config'; \
	rsync -av --delete --exclude '/config/' --exclude '/config.json' "$$stage"/ $(REMOTE):$(REMOTE_DIR)/; \
	ssh $(REMOTE) 'cd $(REMOTE_DIR) && PUID=$$(id -u) PGID=$$(id -g) docker compose up -d --build'

logs:
	ssh $(REMOTE) 'cd $(REMOTE_DIR) && docker compose logs -f stewarr'
