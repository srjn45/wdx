VERSION ?= $(shell git describe --tags 2>/dev/null | sed 's/^v//' || true)
ifeq ($(VERSION),)
VERSION := 0.0.0-dev
endif
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)

PKG     := github.com/srjn45/warden/internal/identity
LDFLAGS := -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT)

.PHONY: build test dist install uninstall clean

# Build for this machine only.
build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/wdx ./cmd/wdx

# Run every test in parallel, with the race detector and in shuffled order.
test:
	go test -race -shuffle=on ./...

# Build release archives + checksums into dist/, laid out like GitHub Releases.
dist:
	scripts/dist.sh $(VERSION) $(COMMIT)

# Install through the real installer, reading the release from dist/ instead of GitHub.
install: dist
	WDX_DOWNLOAD_BASE=file://$(CURDIR)/dist scripts/install.sh

uninstall:
	scripts/install.sh --uninstall

clean:
	rm -rf bin dist
