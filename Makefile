COMPOSE_FILE := development/infrastructure/docker-compose.yml
BINARY_DIR := bin
GATEWAY_BINARY := $(BINARY_DIR)/gateway

.PHONY: build generate infra.up infra.down run test benchmark seed.tokens demo
.PHONY: lint

build:
	mkdir -p $(BINARY_DIR)
	go build -o $(GATEWAY_BINARY) ./cmd/gateway

generate:
	go generate ./openapi/v1

infra.up:
	docker compose --env-file .env -f $(COMPOSE_FILE) up -d --build --wait

infra.down:
	docker compose --env-file .env -f $(COMPOSE_FILE) down

run:
	$(GATEWAY_BINARY)

lint:
	test -z "$$(gofmt -l $$(find . -name '*.go' -not -path './.gocache/*'))"
	go tool golangci-lint run --enable godot --enable dupl --enable revive --enable gocritic --enable noctx

test:
	go test ./...

benchmark:
	go test -bench=. -benchmem ./...

seed.tokens:
	go run ./cmd/token-seed

demo:
	sh ./development/infrastructure/demo.sh
