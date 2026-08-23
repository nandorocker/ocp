PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin
TARGET ?= ocp

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
	go build -o "$(TARGET)" ./cmd/ocp

clean:
	rm -f "$(TARGET)"
	GOOS=darwin GOARCH=arm64 go build -o "$(TARGET)-darwin-arm64" ./cmd/ocp 2>/dev/null || true
