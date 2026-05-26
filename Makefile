BINARY := oi
OUT := bin/$(BINARY)
INSTALL_DIR ?= $(HOME)/.local/bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.PHONY: build install uninstall test clean run doctor models version

build:
	mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o $(OUT) ./cmd/oi

install:
	mkdir -p $(INSTALL_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(INSTALL_DIR)/$(BINARY) ./cmd/oi

uninstall:
	rm -f $(INSTALL_DIR)/$(BINARY)

test:
	go test ./...

clean:
	rm -rf bin

run:
	go run ./cmd/oi

doctor:
	go run ./cmd/oi doctor

models:
	go run ./cmd/oi models

version:
	go run -ldflags "$(LDFLAGS)" ./cmd/oi version
