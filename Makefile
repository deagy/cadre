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
cross-build:
	@mkdir -p dist
	GOOS=linux   GOARCH=amd64 go build -o dist/cadre-linux-amd64       ./cmd/cadre
	GOOS=linux   GOARCH=arm64 go build -o dist/cadre-linux-arm64       ./cmd/cadre
	GOOS=darwin  GOARCH=amd64 go build -o dist/cadre-darwin-amd64      ./cmd/cadre
	GOOS=darwin  GOARCH=arm64 go build -o dist/cadre-darwin-arm64      ./cmd/cadre
	GOOS=windows GOARCH=amd64 go build -o dist/cadre-windows-amd64.exe ./cmd/cadre
