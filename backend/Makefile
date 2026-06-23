APP_NAME=komo-sahvah
BUILD_DIR=bin

.PHONY: test lint align help

all: test build

# Run linter
lint:
	@echo "Running linter..."
# 	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	golangci-lint run --fix

# Fix field alignment issues
align:
	@echo "Fixing field alignment issues..."
# 	go install golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@latest
	~/go/bin/fieldalignment -fix ./...

# Run tests
test:
	@echo "Running tests..."
	go test -v ./...

# Print help information
help:
	@echo "Available targets:"
	@echo "  lint           - Run linter"
	@echo "  align          - Fix field alignment issues"
	@echo "  test           - Run tests"
	@echo "  help           - Print this help information"
        