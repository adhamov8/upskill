мSHELL := /usr/bin/env bash
.ONESHELL:

export GOFLAGS=-mod=mod

.PHONY: dev dev-up dev-down lint test build run fmt tidy

dev: dev-up

dev-up:
	docker compose up -d --build

dev-down:
	docker compose down -v

build:
	docker compose build

run:
	go run ./cmd/app

lint:
	go vet ./...
	golangci-lint run || true

test:
	go test ./...

fmt:
	go fmt ./...

tidy:
	go mod tidy
