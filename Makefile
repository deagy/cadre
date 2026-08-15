# Build/test/lint targets for the Cadre CLI Go port.
#
# Per ADR-001-CLI-GO-REFACTOR.md and CADRE_CLI_GO_ARCHITECTURE.md. Tools
# (goimports, golangci-lint) are pinned in go.mod's `tool` block, per
# library-standards.yaml's version_policy: pin_exact_reviewed_tool_version --
# invoked here via `go tool`, never a floating `@latest` install, so a local
# run and CI resolve the exact same tool binary go.sum already verified.
#
# All build output goes under dist/, never bin/ -- bin/ is this
# repository's existing, committed Python dispatcher (bin/cadre,
# bin/cadre.py, bin/subcommands.tsv, bin/agentic-sdlc, ...); a Go build
# artifact placed there would collide with and overwrite those files.
# dist/ is gitignored.

.PHONY: build test test-race lint fmt vet tidy clean cross-build

build:
	go build -o dist/cadre ./cmd/cadre

# Race requires cgo; CI and local dev are expected to have a C toolchain
# available. If not, fall back to `go test ./...` (no -race) rather than
# disabling cgo globally, since that would silently drop race coverage.
test-race:
	CGO_ENABLED=1 go test -race ./...

test:
	go test ./...

fmt:
	gofmt -l -w .
	go tool goimports -w .

vet:
	go vet ./...

lint: vet
	go tool golangci-lint run ./...

tidy:
	go mod tidy

clean:
	rm -rf dist/

# Cross-platform release build matrix. Signing, SBOMs, and checksums are a
# CI/release-pipeline concern (see team-profile.yaml's cicd.artifact_signing)
# and deliberately not implemented here.
#
# CGO_ENABLED=1 is not optional here, unlike a plain `go build`/`go test`.
# The knowledge store (github.com/mattn/go-sqlite3, see library-standards.yaml)
# requires cgo; a CGO_ENABLED=0 binary still builds and links (go-sqlite3
# ships a cgo-less stub) but every `cadre knowledge ...` call fails at
# runtime with "Binary was compiled with 'CGO_ENABLED=0', go-sqlite3
# requires cgo to work. This is a stub" -- confirmed against this checkout.
# That failure is silent at build time, so it will not show up as a build
# error if this ever regresses; it can only be caught by actually invoking
# `cadre knowledge` against the produced binary. Each GOARCH below therefore
# needs its own C cross-compiler (e.g. a musl/glibc arm64 gcc, an
# osxcross-equivalent for darwin targets, mingw-w64 for windows); this
# target assumes such a toolchain is already on PATH per GOOS/GOARCH and
# does not install one. If no matching CC is available for a given
# GOOS/GOARCH pair, that line fails loudly at link time rather than
# silently downgrading to a cgo-less stub binary.
#
# windows-arm64 is deliberately NOT built here, and this is not an
# oversight: GitHub's hosted windows-latest runner is x64, its gcc is x86_64
# MinGW, and cannot emit ARM64 Windows objects -- with CGO_ENABLED=1 forced
# above, that leg fails to build outright rather than merely being
# untested. Because the release workflow's publish job depends on every
# build leg succeeding, an attempted windows-arm64 leg would fail every
# future release, not just skip one platform. Re-adding it requires
# provisioning a real ARM64 Windows runner (or an equivalent cross
# toolchain) first; see DISTRIBUTION.md's "Platform support" section and
# plugin/tools/binary_platforms.py's module docstring, which record this as
# a decided exclusion, not a gap to fill in later without re-deciding it.
# Five platforms is the contract; plugin/tools/test_binary_shim_contract.py
# guards this list against plugin/tools/binary_platforms.py.
cross-build:
	@mkdir -p dist
	CGO_ENABLED=1 GOOS=linux   GOARCH=amd64 go build -o dist/cadre-linux-amd64         ./cmd/cadre
	CGO_ENABLED=1 GOOS=linux   GOARCH=arm64 go build -o dist/cadre-linux-arm64         ./cmd/cadre
	CGO_ENABLED=1 GOOS=darwin  GOARCH=amd64 go build -o dist/cadre-darwin-amd64        ./cmd/cadre
	CGO_ENABLED=1 GOOS=darwin  GOARCH=arm64 go build -o dist/cadre-darwin-arm64        ./cmd/cadre
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 go build -o dist/cadre-windows-amd64.exe   ./cmd/cadre

# --- pip/pipx distribution -------------------------------------------------
#
# Phase 2 of PYTHON_ELIMINATION_PLAN.md: `pip install cadre` ships the Go
# binary, not Python. One wheel per platform, each carrying its own binary
# plus the roster data that binary reads.
#
# Two steps rather than one, because hatchling cannot do the second:
# shared-data maps *files only*, and a directory source produces no files and
# no error -- a wheel that builds cleanly and is missing every role
# definition. So hatchling packs the binary, and this unpacks the result,
# copies the data tree in, and repacks it. `wheel pack` regenerates RECORD,
# so the hashes stay correct.
#
# Copying with exclusions, rather than enumerating 583 paths, is what keeps a
# newly added role from silently missing the next release.
#
# Usage:
#   make wheel                                  # host platform
#   make wheel GOOS=linux GOARCH=arm64 PLATFORM_TAG=manylinux_2_17_aarch64
WHEEL_PYTHON ?= python3
PLATFORM_TAG ?=
# Set for a Windows wheel. pip copies .data/scripts/* into Scripts/ verbatim,
# and an extensionless file there is not executable on Windows -- so the
# script is renamed during the repack rather than being named per-platform in
# pyproject.toml, which is static and cannot vary by target.
WHEEL_EXE_SUFFIX ?=
# Which binary to package. Defaults to a host build; the release workflow
# passes the cross-compiled one it already produced for this platform.
WHEEL_BINARY ?=

# Paths under the checkout that an installed binary reads at runtime.
# roster/ minus its development-only subtrees, plus the skills and the
# provider bundle. Excludes are stated once here and nowhere else.
WHEEL_DATA_EXCLUDES = \
	--exclude='test' --exclude='tests' --exclude='examples' --exclude='runs' \
	--exclude='proposed-knowledge' --exclude='__pycache__' --exclude='*.py' \
	--exclude='*.pyc' --exclude='.venv' --exclude='data/store.db'

wheel:
	@# Only the wheel artifacts, not all of dist/: the release workflow builds
	@# every platform's wheel from binaries that are already sitting there, and
	@# clearing the directory would delete its own inputs.
	@rm -rf dist-staging build/wheel
	@rm -f dist/*.whl
	@mkdir -p dist
	@mkdir -p dist-staging
	@if [ -n "$(WHEEL_BINARY)" ]; then \
	  cp "$(WHEEL_BINARY)" dist-staging/cadre; \
	else \
	  CGO_ENABLED=1 go build -o dist-staging/cadre ./cmd/cadre; \
	fi
	$(WHEEL_PYTHON) -m build --wheel --outdir dist
	@# Unpack, add the data tree, repack. `wheel pack` rewrites RECORD.
	@mkdir -p build/wheel
	$(WHEEL_PYTHON) -m wheel unpack dist/*.whl --dest build/wheel
	@set -eu; \
	  unpacked=$$(find build/wheel -mindepth 1 -maxdepth 1 -type d); \
	  datadir=$$(find $$unpacked -mindepth 1 -maxdepth 1 -type d -name '*.data')/data/share/cadre; \
	  mkdir -p $$datadir; \
	  tar -c $(WHEEL_DATA_EXCLUDES) VERSION roster .agents/skills provider | tar -x -C $$datadir; \
	  if [ -n "$(WHEEL_EXE_SUFFIX)" ]; then \
	    scripts=$$(find $$unpacked -type d -name scripts); \
	    mv $$scripts/cadre $$scripts/cadre$(WHEEL_EXE_SUFFIX); \
	  fi; \
	  rm -f dist/*.whl; \
	  $(WHEEL_PYTHON) -m wheel pack $$unpacked --dest-dir dist
	@# Retag. A wheel carrying a native binary is not pure Python, and
	@# publishing it as py3-none-any would serve a linux binary to macOS.
	@if [ -n "$(PLATFORM_TAG)" ]; then \
	  $(WHEEL_PYTHON) -m wheel tags --platform-tag $(PLATFORM_TAG) --remove dist/*.whl; \
	fi
	@ls -la dist/*.whl

.PHONY: wheel
