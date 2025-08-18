# Makefile for ddup

BINARY_NAME=ddup
VERSION?=1.0.0
BUILD_TIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S')
GIT_COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")

# Build flags
LDFLAGS=-ldflags "-X github.com/italypaleale/ddup/pkg/buildinfo.AppVersion=$(VERSION) -X github.com/italypaleale/ddup/pkg/buildinfo.BuildTime=$(BUILD_TIME) -X github.com/italypaleale/ddup/pkg/buildinfo.CommitHash=$(GIT_COMMIT)"

.PHONY: build clean test run run-test help install

# Default target
all: build

# Build the application
build:
	@echo "Building $(BINARY_NAME)..."
	mkdir -p dist
	go build $(LDFLAGS) -o dist/$(BINARY_NAME) ./cmd/ddup

# Build for multiple platforms
build-all:
	@echo "Building for multiple platforms..."
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-linux-amd64 ./cmd/ddup
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-darwin-amd64 ./cmd/ddup
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-darwin-arm64 ./cmd/ddup
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-windows-amd64.exe ./cmd/ddup

# Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -f dist/*

# Run tests
test:
	@echo "Running tests..."
	go test ./...

# Run the application with default config
run: build
	./dist/$(BINARY_NAME)

# Run the application with test config
run-test: build
	DDUP_CONFIG=config-test.yaml \
	  ./dist/$(BINARY_NAME)

# Install dependencies
deps:
	@echo "Installing dependencies..."
	go mod tidy
	go mod verify

# Format code
fmt:
	@echo "Formatting code..."
	go fmt ./...

# Lint code (requires golangci-lint)
lint:
	@echo "Linting code..."
	golangci-lint run

# Show help
help:
	@echo "Available targets:"
	@echo "  build      - Build the application"
	@echo "  build-all  - Build for multiple platforms"
	@echo "  clean      - Clean build artifacts"
	@echo "  test       - Run tests"
	@echo "  run        - Build and run with default config"
	@echo "  run-test   - Build and run with test config"
	@echo "  deps       - Install and verify dependencies"
	@echo "  fmt        - Format code"
	@echo "  lint       - Lint code (requires golangci-lint)"
	@echo "  help       - Show this help"
