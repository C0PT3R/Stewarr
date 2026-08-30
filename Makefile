SHELL := /bin/sh

REMOTE ?= user@your-server
REMOTE_DIR ?= ./servarr/connarr

.PHONY: build run fmt test test-browser deploy logs

build:
	go build ./cmd/connarr

run:
	go run ./cmd/connarr -config ./config.json

fmt:
	gofmt -w ./cmd ./internal

test:
	go test ./...

test-browser:
	node tests/ui/reactivity.mjs

deploy:
	@stage=$$(mktemp -d); \
	trap 'rm -rf "$$stage"' EXIT HUP INT TERM; \
	tar -cf - -T .release-manifest | tar -xf - -C "$$stage"; \
	ssh $(REMOTE) 'mkdir -p $(REMOTE_DIR)/config'; \
	rsync -av --delete --exclude '/config/' --exclude '/config.json' "$$stage"/ $(REMOTE):$(REMOTE_DIR)/; \
	ssh $(REMOTE) 'cd $(REMOTE_DIR) && PUID=$$(id -u) PGID=$$(id -g) docker compose up -d --build'

logs:
	ssh $(REMOTE) 'cd $(REMOTE_DIR) && docker compose logs -f connarr'
