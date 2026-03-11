.PHONY: build test clean install lint hooks

BINARY := agentctl
BUILD_DIR := ./build
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -s -w -X main.Version=$(VERSION)

build:
	go build -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/$(BINARY) ./cmd/agentctl

test:
	go test ./... -v

clean:
	rm -rf $(BUILD_DIR)

install: build
	cp $(BUILD_DIR)/$(BINARY) $(HOME)/.local/bin/$(BINARY)

lint:
	go vet ./...

hooks:
	cp scripts/pre-push-release-review.sh .git/hooks/pre-push
	chmod +x .git/hooks/pre-push
	@echo "hooks installed"
