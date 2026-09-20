.PHONY: build test lint install install-signed-macos clean

MACOS_CODESIGN_IDENTIFIER ?= com.printing-press.1password-pp-cli
MACOS_INSTALL_PATH ?= $(HOME)/.local/bin/1password-pp-cli

build:
	go build -o bin/1password-pp-cli ./cmd/1password-pp-cli

test:
	go test ./...

lint:
	golangci-lint run

install:
	go install ./cmd/1password-pp-cli

install-signed-macos:
	@test "$$(uname -s)" = "Darwin" || { echo "install-signed-macos requires macOS" >&2; exit 1; }
	@test -n "$(MACOS_CODESIGN_IDENTITY)" || { echo "set MACOS_CODESIGN_IDENTITY to a Developer ID Application identity" >&2; exit 1; }
	@mkdir -p "$(dir $(MACOS_INSTALL_PATH))"
	go build -o "$(MACOS_INSTALL_PATH).new" ./cmd/1password-pp-cli
	codesign --force --options runtime --timestamp --identifier "$(MACOS_CODESIGN_IDENTIFIER)" --sign "$(MACOS_CODESIGN_IDENTITY)" "$(MACOS_INSTALL_PATH).new"
	codesign --verify --strict --verbose=2 "$(MACOS_INSTALL_PATH).new"
	mv "$(MACOS_INSTALL_PATH).new" "$(MACOS_INSTALL_PATH)"

clean:
	rm -rf bin/

build-mcp:
	go build -o bin/1password-pp-mcp ./cmd/1password-pp-mcp

install-mcp:
	go install ./cmd/1password-pp-mcp

build-all: build build-mcp
