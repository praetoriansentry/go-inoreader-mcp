BINARY   := inoreader-mcp
MODULE   := github.com/praetoriansentry/go-inoreader-mcp
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
IMAGE    ?= ghcr.io/praetoriansentry/go-inoreader-mcp
LDFLAGS  := -s -w -X main.version=$(VERSION)

.PHONY: all build test lint vet fmt cover docker docker-run clean tidy vuln check hooks

all: check build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/$(BINARY)

test:
	go test -race -count=1 ./...

cover:
	go test -race -coverprofile=coverage.out ./... && go tool cover -func=coverage.out | tail -1

vet:
	go vet ./...

fmt:
	gofmt -l . | tee /dev/stderr | test -z "$$(cat)"

tidy:
	go mod tidy && git diff --exit-code go.mod go.sum

vuln:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run ./...

check: fmt vet test

docker:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) -t $(IMAGE):latest .

# Interactive stdio session for a quick manual check.
docker-run: docker
	docker run -i --rm --env-file .env -v inoreader-data:/data $(IMAGE):latest serve

hooks:
	git config core.hooksPath .githooks

clean:
	rm -rf bin coverage.out
