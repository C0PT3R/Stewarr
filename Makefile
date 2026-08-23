SHELL := /bin/sh

REMOTE ?= user@your-server
REMOTE_DIR ?= ./servarr/togetharr

.PHONY: build run fmt test deploy logs

build:
	go build ./cmd/togetharr

run:
	go run ./cmd/togetharr -config ./config.json

fmt:
	gofmt -w ./cmd ./internal

test:
	go test ./...

deploy:
	ssh $(REMOTE) 'mkdir -p $(REMOTE_DIR)/config'
	rsync -av --delete --exclude '.git' --exclude 'config.json' ./ $(REMOTE):$(REMOTE_DIR)/
	ssh $(REMOTE) 'cd $(REMOTE_DIR) && PUID=$$(id -u) PGID=$$(id -g) docker compose up -d --build'

logs:
	ssh $(REMOTE) 'cd $(REMOTE_DIR) && docker compose logs -f togetharr'
