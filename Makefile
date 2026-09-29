PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin
TARGET ?= ocp
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS ?=

export VERSION

.PHONY: install uninstall clean release

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

# release cross-compiles the platform archives consumed by install.sh.
# VERSION must be a release tag without the leading v.
DIST := dist
PLATFORMS := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64

release:
	@test -n "$(VERSION)" || { echo "VERSION is required"; exit 1; }
	@case "$(VERSION)" in *-*) echo "VERSION must be an exact release tag"; exit 1 ;; esac
	@rm -rf "$(DIST)"
	@mkdir -p "$(DIST)"
	@set -e; for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		out="$(DIST)/ocp_$(VERSION)_$${os}_$${arch}"; \
		mkdir -p "$$out"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build \
			-ldflags "$(LDFLAGS) -X main.version=$(VERSION)" \
			-o "$$out/ocp" ./cmd/ocp; \
		tar -czf "$$out.tar.gz" -C "$$out" ocp; \
		rm -rf "$$out"; \
		echo "built $$out.tar.gz"; \
	done
	@cd "$(DIST)" && sha256sum *.tar.gz > checksums.txt
	@cat "$(DIST)/checksums.txt"

clean:
	rm -rf "$(TARGET)" "$(DIST)"
