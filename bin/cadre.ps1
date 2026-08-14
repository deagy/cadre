# Entry point for this repository's `cadre` CLI. Builds (if needed) and
# execs the Go implementation under cmd/cadre -- the authoritative CLI as of
# the Python-to-Go migration (see ADR-001-CLI-GO-REFACTOR.md). See
# README.md "System-wide install" for wrapping this in a $PROFILE function
# so it can be invoked as bare `cadre`.

$RepoRoot = Split-Path -Parent $PSScriptRoot

$GoCommand = Get-Command go -ErrorAction SilentlyContinue
if (-not $GoCommand) { throw "cadre: Go is required to build this checkout's CLI (checked PATH for 'go')" }

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
  New-Item -ItemType Directory -Force -Path $BuildCache | Out-Null
  Push-Location $RepoRoot
  try {
    & $GoCommand.Source build -o $Binary "./cmd/cadre"
    if ($LASTEXITCODE -ne 0) { throw "cadre: build failed" }
  } finally {
    Pop-Location
  }
}

& $Binary @args
exit $LASTEXITCODE
