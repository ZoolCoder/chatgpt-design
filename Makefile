BINARY  := chatgpt-design
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: all build install test race lint fmt clean

all: lint test build

build:
	go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/$(BINARY) ./cmd/$(BINARY)

install:
	go install -trimpath -ldflags "-s -w -X main.version=$(VERSION)" ./cmd/$(BINARY)

test:
	go test ./...

race:
	go test -race ./...

lint:
	gofmt -l . | tee /dev/stderr | (! read)
	go vet ./...

fmt:
	gofmt -w .

clean:
	rm -rf bin dist
