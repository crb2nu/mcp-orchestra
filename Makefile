.PHONY: build test lint run clean setup tidy fmt ci openapi

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

build:
	go build $(LDFLAGS) -o bin/orchestra ./cmd/orchestra

test: tidy
	go test -race -cover ./...

lint:
	golangci-lint run ./...

run: build
	./bin/orchestra serve --registry ./testdata/registry.yaml

validate-example: build
	./bin/orchestra validate --file ./testdata/example-task.yaml --registry ./testdata/registry.yaml

clean:
	rm -rf bin/

# Development helpers
dev:
	go run ./cmd/orchestra serve --registry ./testdata/registry.yaml --log-level debug

fmt:
	go fmt ./...
	goimports -w .

tidy:
	go mod tidy

setup: tidy
	go mod download

openapi:
	./scripts/validate-openapi.sh

ci: tidy test lint openapi
