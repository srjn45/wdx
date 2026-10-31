VERSION ?= $(shell git describe --tags 2>/dev/null | sed 's/^v//' || true)
ifeq ($(VERSION),)
VERSION := 0.0.0-dev
endif
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)

GIT_CLIFF_VERSION := 2.14.2
GIT_CLIFF         := bin/git-cliff-$(GIT_CLIFF_VERSION)

GOLANGCI_LINT_VERSION := 2.15.0
GOLANGCI_LINT         := bin/golangci-lint-$(GOLANGCI_LINT_VERSION)
ACTIONLINT_VERSION    := 1.7.12
ACTIONLINT            := bin/actionlint-$(ACTIONLINT_VERSION)
GOVULNCHECK_VERSION   := 1.9.0
GOVULNCHECK           := bin/govulncheck-$(GOVULNCHECK_VERSION)
SHELLCHECK_VERSION    := 0.11.0
SHELLCHECK            := bin/shellcheck-$(SHELLCHECK_VERSION)

# The commits make check reads; CI sets it to those of the push or PR.
RANGE ?= origin/main..HEAD

IDENTITY := github.com/srjn45/wdx/internal/identity
LDFLAGS  := -s -w -X $(IDENTITY).Version=$(VERSION) -X $(IDENTITY).Commit=$(COMMIT)

.PHONY: build generate test test-stress test-contention test-changed test-update cover lint check tools notices vt-sync dist install uninstall release release-dev changelog clean

# Build for this machine only.
build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/wdx ./cmd/wdx

# Regenerate internal/api from api/openapi.yaml.
generate:
	go generate ./...

# The tests run four packages at a time and, where systemd-run can set one,
# under a memory cap: they must leave room for other work on the machine.
TEST_CAP = $(shell systemd-run --user --scope -q true 2>/dev/null && echo systemd-run --user --scope -q -p MemoryMax=3G -p MemorySwapMax=0)
GO_TEST  = $(TEST_CAP) go test -p 4 -race -shuffle=on

# Run every test, with the race detector and in shuffled order.
test:
	$(GO_TEST) ./...

# Run the tests COUNT times over, at 1 and 4 CPUs, to find the ones that fail
# only now and then; PKG narrows them: make test-stress PKG=./internal/cli COUNT=20.
COUNT ?= 3
PKG   ?= ./...
# Each time is a process of its own: cobra keeps every flag it ever saw, which
# in one process adds up to more than the tests' memory cap.
test-stress:
	@failed=0; for i in $$(seq $(COUNT)); do echo "== $$i of $(COUNT)"; $(GO_TEST) -count=1 -cpu=1,4 -timeout=60m $(PKG) || failed=1; done; exit $$failed

# Run the tests RUNS times at once on a machine kept busy, as when several
# agents test at the same time; PKG narrows them.
RUNS ?= 3
test-contention:
	RUNS=$(RUNS) COUNT=1 PKG="$(PKG)" GO_TEST="$(GO_TEST)" scripts/test-contention.sh

# Repeat the tests of the test files changed in RANGE or not yet committed that
# a change can reach, TIMES times over on a machine kept busy: by hand now and
# then, with RANGE naming the commits since the last run.
TIMES ?= 20
test-changed:
	RANGE="$(RANGE)" TIMES=$(TIMES) GO_TEST="$(GO_TEST)" scripts/test-changed.sh

# Update a wdx built from this tree to a release dist.sh builds of it, and
# roll it back, in a temporary home.
test-update:
	scripts/test-update.sh

# Run the tests once with coverage: bin/cover.out, bin/cover.html and the
# per-package summary.
cover:
	mkdir -p bin
	$(GO_TEST) -covermode=atomic -coverpkg=./... -coverprofile=bin/cover.out ./...
	go tool cover -html=bin/cover.out -o bin/cover.html
	scripts/cover.sh bin/cover.out

# Lint the Go code, the shell scripts and the CI workflows.
lint: $(GOLANGCI_LINT) $(SHELLCHECK) $(ACTIONLINT)
	$(GOLANGCI_LINT) run
	$(SHELLCHECK) scripts/*.sh
	$(ACTIONLINT) -shellcheck $(SHELLCHECK)

# Everything CI checks besides the tests: lint, a tidy go.mod, unchanged
# modules, current notices, known vulnerabilities, the code type-checking
# for the other release targets, and the commit messages in RANGE. Type
# checks catch what a build for another OS would, in a fraction of the time.
check: lint $(GOVULNCHECK)
	go mod tidy -diff
	go mod verify
	scripts/notices.sh
	@git diff --quiet -- THIRD-PARTY-NOTICES.md || { echo "error: THIRD-PARTY-NOTICES.md is out of date; commit it as make notices wrote it" >&2; exit 1; }
	$(GOVULNCHECK) ./...
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go vet ./...
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go vet ./...
	scripts/commits.sh $(RANGE)

# Fetch the pinned linters into bin/.
tools: $(GOLANGCI_LINT) $(SHELLCHECK) $(ACTIONLINT) $(GOVULNCHECK)

# Write THIRD-PARTY-NOTICES.md from the modules linked into wdx.
notices:
	scripts/notices.sh

# Rebase the vt fork onto upstream and move wdx to it (docs/VT.md).
vt-sync:
	scripts/vt-sync.sh

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
	scripts/fetch-release.sh git-cliff $(GIT_CLIFF_VERSION) $@

$(GOLANGCI_LINT):
	scripts/fetch-go-tool.sh github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v$(GOLANGCI_LINT_VERSION) $@

$(ACTIONLINT):
	scripts/fetch-go-tool.sh github.com/rhysd/actionlint/cmd/actionlint@v$(ACTIONLINT_VERSION) $@

$(GOVULNCHECK):
	scripts/fetch-go-tool.sh golang.org/x/vuln/cmd/govulncheck@v$(GOVULNCHECK_VERSION) $@

$(SHELLCHECK):
	scripts/fetch-release.sh shellcheck $(SHELLCHECK_VERSION) $@

clean:
	rm -rf bin dist
