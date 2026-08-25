PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin
TARGET ?= ocp
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS ?=

export VERSION

.PHONY: install uninstall clean

install: build
	@if [ ! -d "$(BINDIR)" ]; then \
		mkdir -p "$(BINDIR)"; \
	fi
	@cp "$(TARGET)" "$(BINDIR)/$(TARGET)"
	@if ! echo "$$PATH" | grep -qF "$(BINDIR)"; then \
		echo "Warning: $(BINDIR) is not on PATH."; \
		echo "Add 'export PATH=\"$(BINDIR):$$PATH\"' to your shell profile."; \
	fi

uninstall:
	rm -f "$(BINDIR)/$(TARGET)"

build:
	go build -ldflags "$(LDFLAGS) -X main.version=$$VERSION" -o "$(TARGET)" ./cmd/ocp

clean:
	rm -f "$(TARGET)" "$(TARGET)-darwin-arm64"
