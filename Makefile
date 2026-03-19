.PHONY: build run test lint clean

BINARY := watchpost
BUILD_DIR := ./build

build:
	go build -o $(BUILD_DIR)/$(BINARY) ./cmd/watchpost

run: build
	$(BUILD_DIR)/$(BINARY) -config config.example.yml

test:
	go test ./...

lint:
	golangci-lint run ./...

clean:
	rm -rf $(BUILD_DIR)
	rm -f watchpost.db

fmt:
	gofmt -w .

check: lint test
