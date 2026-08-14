# Entry point for this repository's `cadre` CLI. Builds (if needed) and
# execs the Go implementation under cmd/cadre -- the authoritative CLI as of
# the Python-to-Go migration (see ADR-001-CLI-GO-REFACTOR.md). See
# README.md "System-wide install" for wrapping this in a $PROFILE function
# so it can be invoked as bare `cadre`.

$RepoRoot = Split-Path -Parent $PSScriptRoot

$BuildCache = if ($env:CADRE_BUILD_CACHE) { $env:CADRE_BUILD_CACHE } else { Join-Path $RepoRoot ".cadre-build-cache" }
$Binary = Join-Path $BuildCache "cadre.exe"

$NeedsBuild = $true
if (Test-Path $Binary) {
  $BinaryTime = (Get-Item $Binary).LastWriteTimeUtc
  $NewestSource = Get-ChildItem -Path (Join-Path $RepoRoot "cmd"), (Join-Path $RepoRoot "internal") -Filter "*.go" -Recurse -ErrorAction SilentlyContinue |
    Where-Object { $_.LastWriteTimeUtc -gt $BinaryTime } |
    Select-Object -First 1
  if (-not $NewestSource) { $NeedsBuild = $false }
}

if ($NeedsBuild) {
  # Resolved here, not up front: a warm cache needs no toolchain, and
  # demanding one anyway made `cadre` unusable under a deliberately narrowed
  # PATH (see bin/cadre for the case that surfaced it).
  $GoCommand = Get-Command go -ErrorAction SilentlyContinue
  if (-not $GoCommand) { throw "cadre: Go is required to build this checkout's CLI (checked PATH for 'go')" }
  New-Item -ItemType Directory -Force -Path $BuildCache | Out-Null
  Push-Location $RepoRoot
  $PreviousCgo = $env:CGO_ENABLED
  try {
    # Silent on success, verbatim on failure. `go build` writes progress to
    # stderr on a cold module cache ("go: downloading ..."), which is
    # indistinguishable from CLI diagnostics to anything reading this
    # wrapper's stderr -- it breaks `cadre --version`'s "writes nothing to
    # stderr" contract. Buffering loses nothing: a real build failure is
    # replayed in full before throwing.
    # cgo first, then a cgo-less retry -- see bin/cadre for the full
    # reasoning. Short version: `cadre knowledge` needs the cgo-backed
    # sqlite3 driver, a CGO_ENABLED=0 binary builds fine but fails every
    # knowledge call at runtime with no build-time signal, and forcing
    # CGO_ENABLED=1 unconditionally would break the whole build on a machine
    # with no C toolchain (the common case on Windows). Prefer the full
    # binary, accept the degraded one, and let `cadre doctor` say which.
    $env:CGO_ENABLED = "1"
    $BuildOutput = & $GoCommand.Source build -o $Binary "./cmd/cadre" 2>&1
    if ($LASTEXITCODE -ne 0) {
      $env:CGO_ENABLED = "0"
      $BuildOutput = & $GoCommand.Source build -o $Binary "./cmd/cadre" 2>&1
      if ($LASTEXITCODE -ne 0) {
        $BuildOutput | ForEach-Object { [Console]::Error.WriteLine($_) }
        throw "cadre: failed to build the Go CLI from $RepoRoot"
      }
    }
  } finally {
    Pop-Location
    # CGO_ENABLED is a build-time selector, not something the CLI process
    # should inherit -- restore whatever the caller had (including unset), so
    # a warm-cache run and a cold-build run hand the binary the same
    # environment.
    if ($null -eq $PreviousCgo) {
      Remove-Item Env:\CGO_ENABLED -ErrorAction SilentlyContinue
    } else {
      $env:CGO_ENABLED = $PreviousCgo
    }
  }
}

# Tell the built binary which checkout produced it -- see bin/cadre for why
# a working-directory walk cannot answer this.
$env:CADRE_REPO_ROOT = $RepoRoot

& $Binary @args
exit $LASTEXITCODE
