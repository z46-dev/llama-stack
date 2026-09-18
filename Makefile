SHELL := /usr/bin/bash

GO ?= go
VERSION ?= $(shell git describe --always --dirty 2>/dev/null || printf 'development')
BUILD_DIR ?= build
SETUP_FLAGS ?=
GOFLAGS := -trimpath
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all build clean fmt install lint setup test verify

all: verify build

build:
	mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/llama-stack ./cmd/llama-stack
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/llama-stackd ./cmd/llama-stackd
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/llama-stack-resource-mcp ./cmd/llama-stack-resource-mcp

fmt:
	$(GO) fmt ./...

test:
	$(GO) test ./...

lint:
	$(GO) vet ./...
	shellcheck scripts/*.sh

verify: fmt test lint

# Installs already-built stack files. Use `sudo make install`.
install: build
	./scripts/install-host.sh --skip-packages --skip-llama-build

# Fedora 44 end-to-end setup. Use `sudo make setup`.
setup:
	./scripts/install-host.sh $(SETUP_FLAGS)

clean:
	$(RM) -r $(BUILD_DIR)
