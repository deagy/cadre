# Cadre CLI Distribution Strategy

## Overview

The Cadre CLI has been refactored from Python to Go, eliminating the Python 3.10+ runtime dependency. This document outlines the distribution strategy for shipping the Go binary across multiple platforms and package managers.

**Goal**: Reduce installation friction and startup latency by distributing a single static binary instead of interpreted Python code with runtime requirements.

## Current Status

- **Python CLI**: `bin/cadre` (shell), `bin/cadre.ps1` (PowerShell), `bin/cadre.py` (dispatcher)
  - Requires Python 3.10+ installed
  - Cross-platform but with platform-specific wrappers
  - Slow startup (~100-200ms Python interpreter startup)

- **Go CLI**: Single binary in `cmd/cadre/`
  - Platform-independent implementation
  - Conditional OS-specific code (POSIX/Windows paths)
  - Near-instant startup (<1ms)
  - No runtime dependencies (static binary possible)

## Distribution Channels

### 1. Direct Binary Download (Recommended for Air-Gapped)

**Approach**: GitHub Releases with pre-built binaries for all major platforms.

**Artifacts** (per release):
```
cadre-v0.24.0-linux-amd64.tar.gz
cadre-v0.24.0-linux-arm64.tar.gz
cadre-v0.24.0-darwin-amd64.tar.gz
cadre-v0.24.0-darwin-arm64.tar.gz
cadre-v0.24.0-windows-amd64.zip
cadre-v0.24.0-windows-arm64.zip
```

**Build Process**:
```bash
# Cross-platform build via Makefile
make cross-build

# Signature verification
gpg --detach-sign cadre-v0.24.0-linux-amd64.tar.gz
```

**Installation**:
```bash
# Automated installer script (get.cadre.dev)
curl -fsSL https://get.cadre.dev/install.sh | bash

# Manual download
wget https://github.com/deagy/cadre/releases/download/v0.24.0/cadre-v0.24.0-linux-amd64.tar.gz
tar xzf cadre-v0.24.0-linux-amd64.tar.gz
sudo mv cadre /usr/local/bin/
```

**Advantages**:
- Works in air-gapped environments
- Signature verification available
- No external dependency on package managers
- Complete control over release schedule

### 2. Homebrew (macOS & Linux)

**Approach**: Maintain a Homebrew formula in a custom tap or community-homebrew.

**Installation**:
```bash
brew install deagy/cadre/cadre
# or
brew tap deagy/cadre
brew install cadre
```

**Formula Location**: `deagy/homebrew-cadre/Formula/cadre.rb`

**Contents**:
```ruby
class Cadre < Formula
  desc "Agent orchestration CLI (Go-based)"
  homepage "https://github.com/deagy/cadre"
  url "https://github.com/deagy/cadre/releases/download/v0.24.0/cadre-v0.24.0-darwin-amd64.tar.gz"
  sha256 "..."
  
  def install
    bin.install "cadre"
  end
end
```

**Advantages**:
- Standard macOS distribution
- Automatic updates via `brew upgrade`
- Familiar workflow for Homebrew users

**Timeline**: Post-release, once binary distribution is stable.

### 3. Linux Distributions (apt, dnf, pacman)

**Approach**: Publish packages to distribution-specific repositories.

#### Ubuntu/Debian (apt)
Repository: `ppa:deagy/cadre` (optional; or manual `.deb` packages)

```bash
sudo apt-add-repository ppa:deagy/cadre
sudo apt-get update
sudo apt-get install cadre
```

#### Fedora/RHEL (dnf/yum)
Repository: Copr (Fedora Community Projects)

```bash
sudo dnf copr enable deagy/cadre
sudo dnf install cadre
```

#### Arch (pacman)
Package: AUR (Arch User Repository) or community-maintained

```bash
yay -S cadre
# or
git clone https://aur.archlinux.org/cadre.git
cd cadre
makepkg -si
```

**Advantages**:
- Native package management for each distro
- Automatic updates via system package manager
- Dependency resolution handled by distro

**Timeline**: Post-release, if there is demand.

### 4. PyPI (Backward Compatibility)

**Approach**: Continue distributing via PyPI as a convenience wrapper, but ship the Go binary instead of Python code.

**Installation** (existing users):
```bash
pip install cadre
# or
pipx install cadre
```

**Contents**:
```
cadre-0.24.0/
├── setup.py
├── setup.cfg
├── cadre/
│   ├── __init__.py (entry point)
│   └── bin/
│       ├── cadre-linux-amd64 (Go binary)
│       ├── cadre-darwin-amd64 (Go binary)
│       ├── cadre-windows-amd64.exe (Go binary)
│       └── ...
└── README.md
```

**Behavior**:
```python
# cadre/__init__.py
import sys
import platform
import os
from pathlib import Path

# Select appropriate binary for current platform
binary = Path(__file__).parent / "bin" / f"cadre-{platform.system()}-{platform.machine()}"
os.execv(str(binary), sys.argv)
```

**Advantages**:
- No breaking change for existing pip/pipx users
- Satisfies Python version requirement checkers (still declares Python 3.10+)
- Gradual transition path

**Timeline**: Release with Go binary (v0.24.0+).

### 5. Docker (Container Distribution)

**Approach**: Publish official Docker image with Go CLI pre-installed.

**Dockerfile**:
```dockerfile
FROM golang:1.21 AS builder
WORKDIR /build
COPY . .
RUN CGO_ENABLED=1 go build -o cadre ./cmd/cadre

FROM alpine:latest
COPY --from=builder /build/cadre /usr/local/bin/
ENTRYPOINT ["cadre"]
```

**Usage**:
```bash
docker run ghcr.io/deagy/cadre:latest help
docker run -v ~/.agents:/root/.agents ghcr.io/deagy/cadre:latest select --task "..." --files a.go,b.ts
```

**Registry**: GitHub Container Registry (ghcr.io)

**Advantages**:
- Consistent environment across platforms
- No local installation required
- Ideal for CI/CD pipelines

**Timeline**: Post-release.

## Release Process

### Pre-Release

1. **Version Bump**: Update version in code + documentation
   ```bash
   # cmd/cadre/main.go, go.mod (if needed), README.md
   ```

2. **Test Matrix**:
   ```bash
   CGO_ENABLED=1 go test ./...
   make cross-build
   ```

3. **Security**: Dependency audit
   ```bash
   go mod tidy
   govulncheck ./...
   ```

### Release

1. **Tag**: `git tag v0.24.0`
2. **Build Binaries**: Cross-platform compilation via `make cross-build`
3. **Sign**: GPG sign each binary
4. **Create Release**: GitHub Release with binaries + checksums
5. **Announce**: Update README, release notes, announcements

### Post-Release

1. **Homebrew**: Update formula to new version
2. **PyPI**: Build and publish wheel with bundled binaries
3. **Distro Repos**: Update recipes if applicable
4. **Docker**: Push new image to ghcr.io

## Build Configuration

### Makefile Targets

```makefile
.PHONY: build cross-build sign install test

build:
	go build -o bin/cadre ./cmd/cadre

cross-build:
	GOOS=linux GOARCH=amd64 go build -o dist/cadre-linux-amd64 ./cmd/cadre
	GOOS=linux GOARCH=arm64 go build -o dist/cadre-linux-arm64 ./cmd/cadre
	GOOS=darwin GOARCH=amd64 go build -o dist/cadre-darwin-amd64 ./cmd/cadre
	GOOS=darwin GOARCH=arm64 go build -o dist/cadre-darwin-arm64 ./cmd/cadre
	GOOS=windows GOARCH=amd64 go build -o dist/cadre-windows-amd64.exe ./cmd/cadre
	GOOS=windows GOARCH=arm64 go build -o dist/cadre-windows-arm64.exe ./cmd/cadre

sign:
	for f in dist/*; do gpg --detach-sign $$f; done

install: build
	sudo cp bin/cadre /usr/local/bin/
	cadre --version
```

### CGO Requirements

**SQLite Dependency**:
- Requires CGO when using `github.com/mattn/go-sqlite3`
- Set `CGO_ENABLED=1` during build
- Cross-compilation with CGO requires cross-compilation toolchain (e.g., mingw for Windows)

**Alternative**: Switch to pure-Go SQLite if CGO becomes problematic (`github.com/modernc.org/sqlite`).

## Platform-Specific Notes

### Linux
- Supports glibc 2.17+ (common across distributions)
- ARM64 builds verified on Raspberry Pi 4+
- Static build possible: `go build -ldflags="-s -w" ./cmd/cadre`

### macOS
- Requires macOS 10.12+ (Sierra) for current Go runtime
- Universal binary (amd64+arm64): Requires additional build configuration
- Signature/notarization: Optional, not required for distribution

### Windows
- Supports Windows 7 SP1+ via Go runtime
- No shell wrapper needed (single .exe)
- Can run from PowerShell or cmd.exe

## Rollback Strategy

If a release has critical issues:
1. Mark release as "pre-release" on GitHub
2. Revert to previous stable version in package managers
3. Publish patch fix (e.g., v0.24.1) and re-release

## Long-Term Maintenance

- **Dependency Updates**: `go get -u ./...` quarterly
- **Security Audits**: `govulncheck` on every PR
- **Platform Support**: Drop support for EOL Go versions annually
- **Breaking Changes**: Major version bump only

## See Also

- `Makefile` — build targets and release automation
- `go.mod`, `go.sum` — dependency management
- `cmd/cadre/main.go` — entry point
- `.github/workflows/release.yml` — GitHub Actions CI/CD for releases

## Roadmap

- [ ] v0.24.0: Initial Go binary distribution (GitHub Releases + PyPI)
- [ ] v0.25.0: Homebrew tap official
- [ ] v0.26.0: Linux distro repos (apt, dnf, pacman)
- [ ] v0.27.0: Official Docker image + registry
- [ ] v1.0.0: Stable API + long-term support
