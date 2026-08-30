# relay-cli — one static binary, no runtime dependencies.
#
# One module, so this is the only Makefile: "how do I test everything" has one
# answer, and it is the same command whichever directory you are in.
#
# CGO is off everywhere on purpose: the point of this binary is that a user
# downloads one file and runs it, with no jq, no curl, no Go toolchain and no
# libc version to match.

BINARY  := relay
PKG     := ./cmd/relay-cli
# The version constant lives inside a const ( … ) block, so this matches the
# indented form. It is the single source of truth: the release workflow refuses
# to publish a tag that disagrees with it.
VERSION := $(shell sed -n 's/^[[:space:]]*version[[:space:]]*=[[:space:]]*"\(.*\)"$$/\1/p' cmd/relay-cli/main.go)
DIST    := dist

# Apple Silicon only for now. darwin/amd64, linux/amd64 and linux/arm64 all
# build fine — CGO is off and there is nothing platform-specific in here — they
# are simply not published yet. Add one to this line when it is supported and
# the release picks it up.
#
# Artifacts are named for the platform a user recognises, not for GOOS: someone
# on a MacBook is looking for "macos-arm64", not "darwin-arm64".
PLATFORMS := darwin/arm64

.PHONY: help hooks all build test vet fmt check dist clean run version

help:
	@echo "make check   gofmt + vet + test — what to run before a PR"
	@echo "make test    go test ./..."
	@echo "make build   build ./$(BINARY)"
	@echo "make fmt     gofmt -w ."
	@echo "make hooks   install the pre-commit hook (one-time, per clone)"
	@echo
	@echo "Release: see docs/development.md"

# Git hooks are not versioned, so they have to be opted into per clone. This
# points git at the tracked .githooks/ directory rather than copying anything,
# so an updated hook reaches you with a pull instead of needing a reinstall.
hooks:
	@git config core.hooksPath .githooks
	@echo "pre-commit hook installed (core.hooksPath = .githooks)"
	@echo "skip it for one commit with: git commit --no-verify"

all: check build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BINARY) $(PKG)

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

# The same set the manual CI workflow runs, minus -race for speed.
check:
	@u=$$(gofmt -l .); \
	if [ -n "$$u" ]; then echo "needs gofmt:"; echo "$$u"; exit 1; fi
	@go vet ./...
	@go test ./...
	@echo "ok"

# Print the version the Makefile resolved. The release workflow reads this, so
# a silently-empty VERSION becomes a failed release rather than an artifact
# called relay--macos-arm64.
version:
	@test -n "$(VERSION)" || { echo "error: could not read the version constant from cmd/relay-cli/main.go" >&2; exit 1; }
	@echo $(VERSION)

# One archive-free binary per platform, named so a release page reads clearly,
# plus a checksum file so a download can be verified.
dist: clean
	@test -n "$(VERSION)" || { echo "error: could not read the version constant from cmd/relay-cli/main.go" >&2; exit 1; }
	@mkdir -p $(DIST)
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		label=$$os; [ "$$os" = "darwin" ] && label=macos; \
		out=$(DIST)/$(BINARY)-$(VERSION)-$$label-$$arch; \
		echo "  $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			go build -trimpath -ldflags="-s -w" -o $$out $(PKG) || exit 1; \
	done
	@# sha256sum on Linux, shasum on macOS — the file is identical either way.
	@cd $(DIST) && if command -v sha256sum >/dev/null; then \
		sha256sum $(BINARY)-* > SHA256SUMS; \
	else \
		shasum -a 256 $(BINARY)-* > SHA256SUMS; \
	fi
	@echo "built $(BINARY) $(VERSION) for: $(PLATFORMS)"
	@echo "checksums: $(DIST)/SHA256SUMS"

# Run against ~/.relay/config — the one place relay-cli reads. `make build`
# first so this is always the binary you just changed, not one on PATH.
run: build
	./$(BINARY) run

clean:
	rm -rf $(DIST) $(BINARY)
