VERSION ?= $(shell git describe --tags 2>/dev/null | sed 's/^v//' || true)
ifeq ($(VERSION),)
VERSION := 0.0.0-dev
endif
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)

GIT_CLIFF_VERSION := 2.14.2
GIT_CLIFF         := bin/git-cliff-$(GIT_CLIFF_VERSION)

PKG     := github.com/srjn45/warden/internal/identity
LDFLAGS := -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT)

.PHONY: build generate test notices dist install uninstall release release-dev changelog clean

# Build for this machine only.
build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/wdx ./cmd/wdx

# Regenerate internal/api from api/openapi.yaml.
generate:
	go generate ./...

# Run every test in parallel, with the race detector and in shuffled order.
test:
	go test -race -shuffle=on ./...

# Write THIRD-PARTY-NOTICES.md from the modules linked into wdx.
notices:
	scripts/notices.sh

# Build release archives + checksums into dist/, laid out like GitHub Releases.
dist:
	scripts/dist.sh $(VERSION) $(COMMIT)

# Install through the real installer, reading the release just built from dist/ instead of GitHub.
install: dist
	WDX_DOWNLOAD_BASE=file://$(CURDIR)/dist WDX_VERSION=$(VERSION) scripts/install.sh

uninstall:
	scripts/install.sh --uninstall

# Tag the next stable release, after committing its CHANGELOG.md, and build it into dist/.
release: $(GIT_CLIFF)
	GIT_CLIFF=$(GIT_CLIFF) scripts/release.sh stable

# Tag HEAD as the next dev release and build it into dist/.
release-dev: $(GIT_CLIFF)
	GIT_CLIFF=$(GIT_CLIFF) scripts/release.sh dev

# Print what changed since the last stable release.
changelog: $(GIT_CLIFF)
	@RUST_LOG=error $(GIT_CLIFF) --unreleased --strip all

$(GIT_CLIFF):
	scripts/fetch-git-cliff.sh $(GIT_CLIFF_VERSION) $@

clean:
	rm -rf bin dist
