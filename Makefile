# Makefile for Reforge
# Go-based CLI batch video conversion tool using FFmpeg
#
# Usage: make [target]
# Run 'make help' for available targets.

# ──────────────────────────────────────────────────────────────────
# Variables
# ──────────────────────────────────────────────────────────────────
BINARY_WIN       := reforge.exe
BINARY_LINUX     := reforge-linux
BINARY_DARWIN    := reforge-darwin
BENCHMARK_WIN    := benchmark.exe
BENCHMARK_LINUX  := benchmark-linux
BENCHMARK_DARWIN := benchmark-darwin
# VERSION can be overridden on the command line: make build VERSION=1.2.3
# Falls back to the current release tag. No sed/date required (Windows-compatible).
VERSION          := $(shell git describe --tags --abbrev=0 2>nul || echo 0.9.1)
VERSION          := $(patsubst v%,%,$(VERSION))
COMMIT           := $(shell git rev-parse --short HEAD 2>nul || echo none)
VERSION_LDFLAGS  := -X main.version=$(VERSION) -X main.commit=$(COMMIT)
LDFLAGS          := -s -w $(VERSION_LDFLAGS)
COVERAGE         := coverage.out

# Native binary name — auto-detected from OS
ifeq ($(OS),Windows_NT)
  BINARY_NAME    := reforge.exe
  BENCHMARK_NAME := benchmark.exe
  WINRES_DEP     := winres
else
  UNAME_S := $(shell uname -s)
  ifeq ($(UNAME_S),Darwin)
    BINARY_NAME    := reforge
    BENCHMARK_NAME := benchmark
  else
    BINARY_NAME    := reforge
    BENCHMARK_NAME := benchmark
  endif
  WINRES_DEP     :=
endif

# ──────────────────────────────────────────────────────────────────
# Default target
# ──────────────────────────────────────────────────────────────────
.DEFAULT_GOAL := help

.PHONY: all \
        build build-windows build-linux build-darwin build-all \
        release release-windows release-linux release-darwin release-all \
        benchmark benchmark-windows benchmark-linux benchmark-darwin benchmark-all \
        test test-verbose test-race test-short test-cover cover cover-html \
        fmt vet lint tidy clean download-sample winres help

all: lint test build  ## Run lint + test + build (auto-detects OS)

# ──────────────────────────────────────────────────────────────────
# Windows resource embedding (icon, version info, manifest)
# ──────────────────────────────────────────────────────────────────
winres:  ## Generate Windows resource files (.syso) with icon and version info
	go-winres make --product-version=git-tag --file-version=git-tag

# ──────────────────────────────────────────────────────────────────
# Build
# ──────────────────────────────────────────────────────────────────
build: $(WINRES_DEP)  ## Development build for current OS (stripped: smaller binary, faster AV scan on launch)
	go build -ldflags="$(LDFLAGS)" -o $(BINARY_NAME) .

build-windows: winres  ## Development build for Windows
	GOOS=windows GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(BINARY_WIN) .

build-linux:  ## Development build for Linux
	GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(BINARY_LINUX) .

build-darwin:  ## Development build for macOS
	GOOS=darwin GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(BINARY_DARWIN) .

build-darwin-arm:  ## Development build for macOS Apple Silicon
	GOOS=darwin GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o $(BINARY_DARWIN)-arm64 .

build-all: build-windows build-linux build-darwin  ## Development build for all platforms

# ──────────────────────────────────────────────────────────────────
# Release (optimised, stripped)
# ──────────────────────────────────────────────────────────────────
release: $(WINRES_DEP)  ## Production build for current OS (stripped symbols, smaller binary)
	go build -ldflags="$(LDFLAGS)" -o $(BINARY_NAME) .

release-windows: winres  ## Production build for Windows
	GOOS=windows GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(BINARY_WIN) .

release-linux:  ## Production build for Linux
	GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(BINARY_LINUX) .

release-darwin:  ## Production build for macOS (Intel)
	GOOS=darwin GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(BINARY_DARWIN) .

release-darwin-arm:  ## Production build for macOS Apple Silicon
	GOOS=darwin GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o $(BINARY_DARWIN)-arm64 .

release-all: release-windows release-linux release-darwin release-darwin-arm  ## Production build for all platforms

# ──────────────────────────────────────────────────────────────────
# Benchmark tool
# ──────────────────────────────────────────────────────────────────
benchmark:  ## Build benchmark tool for current OS
	go build -ldflags="$(LDFLAGS)" -o $(BENCHMARK_NAME) ./cmd/benchmark

benchmark-windows:  ## Build benchmark tool for Windows
	GOOS=windows GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(BENCHMARK_WIN) ./cmd/benchmark

benchmark-linux:  ## Build benchmark tool for Linux
	GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(BENCHMARK_LINUX) ./cmd/benchmark

benchmark-darwin:  ## Build benchmark tool for macOS (Intel)
	GOOS=darwin GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(BENCHMARK_DARWIN) ./cmd/benchmark

benchmark-all: benchmark-windows benchmark-linux benchmark-darwin  ## Build benchmark tool for all platforms

# ──────────────────────────────────────────────────────────────────
# Test
# ──────────────────────────────────────────────────────────────────
test:  ## Run all tests
	@go run ./cmd/download-sample --ensure
	go test ./... -count=1 -timeout 120s

test-verbose:  ## Run tests with verbose output
	go test -v ./...

test-race:  ## Run tests with race detector
	go test -race ./...

test-short:  ## Run only fast unit tests (skip integration tests that need ffmpeg/GPU)
	go test -short ./...

test-cover:  ## Run tests and print per-package coverage summary
	go test -cover ./...

cover:  ## Generate coverage report and open in browser
	go test -coverprofile=$(COVERAGE) ./...
	go tool cover -html=$(COVERAGE)

cover-html: cover  ## Alias for cover (generate HTML report)

# ──────────────────────────────────────────────────────────────────
# Cross-compile check (build only, no run)
# ──────────────────────────────────────────────────────────────────
build-check:  ## Verify the code compiles for Windows, Linux and macOS
	GOOS=windows GOARCH=amd64 go build ./...
	GOOS=linux   GOARCH=amd64 go build ./...
	GOOS=darwin  GOARCH=amd64 go build ./...
	@echo "build-check: all platforms OK"

# ──────────────────────────────────────────────────────────────────
# Code quality
# ──────────────────────────────────────────────────────────────────
fmt:  ## Format code
	go fmt ./...

vet:  ## Run static analysis
	go vet ./...

lint: fmt vet  ## Run fmt + vet

# ──────────────────────────────────────────────────────────────────
# Dependencies
# ──────────────────────────────────────────────────────────────────
tidy:  ## Tidy and verify module dependencies
	go mod tidy
	go mod verify

# ──────────────────────────────────────────────────────────────────
# Cleanup (cross-platform: rm -f works on Linux/macOS/WSL/Git-Bash)
# ──────────────────────────────────────────────────────────────────
clean:  ## Remove build artifacts and coverage files
	go clean
	rm -f $(BINARY_WIN) $(BINARY_LINUX) $(BINARY_DARWIN) $(BINARY_DARWIN)-arm64
	rm -f $(BENCHMARK_WIN) $(BENCHMARK_LINUX) $(BENCHMARK_DARWIN)
	rm -f $(COVERAGE)
	rm -f rsrc_windows_*.syso

# ──────────────────────────────────────────────────────────────────
# Sample video download (Big Buck Bunny)
# ──────────────────────────────────────────────────────────────────
download-sample:  ## Download Big Buck Bunny sample video (interactive)
	go run ./cmd/download-sample

# ──────────────────────────────────────────────────────────────────
# Help
# ──────────────────────────────────────────────────────────────────
help:  ## Print available targets with descriptions
ifeq ($(OS),Windows_NT)
	@powershell -NoProfile -Command "Get-Content $(MAKEFILE_LIST) | Where-Object { $$_ -match '^[a-zA-Z0-9_-]+:.*?## ' } | ForEach-Object { $$p = $$_ -split ':.*?## '; '{0,-26} {1}' -f $$p[0].Trim(), $$p[1].Trim() }"
else
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
	  | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-28s %s\n", $$1, $$2}'
endif
