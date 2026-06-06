# Thin convenience wrapper. GoReleaser (.goreleaser.yaml) is the source of truth for
# cross-platform builds and packaging; these targets just call it / the go toolchain.
# (On Windows, `make` isn't installed by default — run the commands directly, or use the
# GoReleaser binary / GitHub Actions.)

BINARY  := sportshub
PKG     := ./cmd/sportshub
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build run test vet fmt snapshot release check clean tag

## build: compile a host binary (embedded assets) into ./bin
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) $(PKG)

## run: run locally with -tags dev so web/dist is served from disk (live UI edits)
run:
	go run -tags dev $(PKG)

## test / vet / fmt
test:
	go test ./...
vet:
	go vet ./...
fmt:
	gofmt -w cmd internal web

## snapshot: build the full cross-platform matrix + deb/rpm/apk locally into ./dist (no publish)
snapshot:
	goreleaser release --snapshot --clean

## release: tag-driven real release (normally run by CI on a pushed tag)
release:
	goreleaser release --clean

## check: validate the GoReleaser config
check:
	goreleaser check

## tag: compute the next semver from commit messages (svu) and push it, triggering a release.
## Override the version with `make tag V=v0.1.0` (required for the first release).
## Installs svu on demand: https://github.com/caarlos0/svu
tag:
	@command -v svu >/dev/null 2>&1 || go install github.com/caarlos0/svu/v3@latest
	@NEXT="$${V:-$$(svu next)}"; \
	if git rev-parse "$$NEXT" >/dev/null 2>&1; then echo "tag $$NEXT already exists"; exit 1; fi; \
	echo "Releasing $$NEXT"; \
	git log --pretty=format:"  %s" $$(git describe --tags --abbrev=0 2>/dev/null)..HEAD; echo; \
	read -p "Create and push $$NEXT? [y/N] " ok; [ "$$ok" = y ] || exit 1; \
	git tag -a "$$NEXT" -m "Release $$NEXT" && git push origin "$$NEXT"

clean:
	rm -rf dist bin
