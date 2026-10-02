.PHONY: gen run dev test test-unit cover lint docker-build db-up db-down db-reset

gen:
	go tool oapi-codegen -config oapi-codegen.yaml api/openapi.yaml
	go tool sqlc generate

run:
	go run ./cmd/api

dev:
	go tool air -c configs/air.toml 2>/dev/null || air -c configs/air.toml

test:
	go test ./...

test-unit:
	go test -short ./...

# Unit-test coverage of hand-written code (generated code and test helpers are excluded).
cover:
	go test -short -coverprofile=coverage.out ./...
	grep -v -e 'api.gen.go' -e 'db/gen/' -e 'internal/testutil/' coverage.out > coverage.filtered.out
	go tool cover -func=coverage.filtered.out | tail -1

lint:
	golangci-lint run

db-up:
	docker compose -f deployments/compose.yml up -d --wait

db-down:
	docker compose -f deployments/compose.yml down

db-reset:
	docker compose -f deployments/compose.yml down -v && docker compose -f deployments/compose.yml up -d --wait

docker-build:
	docker build -f build/package/Dockerfile -t meta-frames-server .
