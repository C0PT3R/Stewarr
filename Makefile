SHELL := /bin/sh

REMOTE ?= user@your-server
REMOTE_DIR ?= ./servarr/spartarr

.PHONY: build run fmt test deploy logs

build:
	go build ./cmd/spartarr

run:
	go run ./cmd/spartarr -config ./config.json

fmt:
	gofmt -w ./cmd ./internal

test:
	go test ./...

deploy:
	ssh $(REMOTE) 'mkdir -p $(REMOTE_DIR)/config'
	rsync -av --delete --exclude '.git' --exclude 'config.json' ./ $(REMOTE):$(REMOTE_DIR)/
	ssh $(REMOTE) 'cd $(REMOTE_DIR) && PUID=$$(id -u) PGID=$$(id -g) docker compose up -d --build'

logs:
	ssh $(REMOTE) 'cd $(REMOTE_DIR) && docker compose logs -f spartarr'
