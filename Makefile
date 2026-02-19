.PHONY: build test clean install

BINARY := agentctl
BUILD_DIR := ./build

build:
	go build -o $(BUILD_DIR)/$(BINARY) ./cmd/agentctl

test:
	go test ./... -v

clean:
	rm -rf $(BUILD_DIR)

install: build
	cp $(BUILD_DIR)/$(BINARY) $(HOME)/.local/bin/$(BINARY)

lint:
	go vet ./...
