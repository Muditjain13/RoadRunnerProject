SHELL := /bin/bash
COMPOSE := docker compose -f deploy/compose/docker-compose.yml

.PHONY: up down test test-int lint proto migrate-up seed seed-small load chaos

up:
	$(COMPOSE) up -d --build

down:
	$(COMPOSE) down -v

test:
	go test -race ./...

test-int:
	go test -race -tags=integration ./...

lint:
	golangci-lint run ./...

proto:
	buf generate

migrate-up:
	@for svc in inventory booking payment; do \
		echo "migrating $$svc"; \
		var=$$(echo $$svc | tr a-z A-Z)_DSN; \
		migrate -path migrations/$$svc -database "$${!var}" up; \
	done

seed:
	go run ./cmd/seed --profile=full

seed-small:
	go run ./cmd/seed --profile=small

load:
	k6 run test/load/*.js

chaos:
	go run ./cmd/supplier-sim --flight-error-rate=0.4 --flight-p99=2s
