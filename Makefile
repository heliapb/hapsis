.PHONY: test lint lint-install demo demo-down

GOLANGCI_LINT_VERSION:=v2.14.0

test:
	go vet ./...
	go test -race -coverprofile=coverage.out ./...

lint-install:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

lint:
	golangci-lint run ./...

demo:
	docker compose -f demo/docker-compose.yaml up --build -d

demo-down:
	docker compose -f demo/docker-compose.yaml down -v
