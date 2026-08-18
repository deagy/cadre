<#
.SYNOPSIS
  Install Cadre for whichever AI coding runners are on this machine (Windows).

.DESCRIPTION
  The PowerShell counterpart of install.sh. Same marketplace (`cadre-team`),
  same repository, same idempotency and --DryRun/--Uninstall behaviour.

  Windows gets a real native path rather than a "use WSL" apology, because
  bin/cadre.ps1 is a genuine PowerShell launcher. Two things differ from the
  POSIX script, both forced by the platform:

    * A `cadre.cmd` shim is written instead of a symlink. Symlink creation on
      Windows needs either Developer Mode or an elevated shell, and an
      installer should not require either.
    * The bin directory is added to the *user* PATH via the registry-backed
      environment, which only affects new shells -- so this reports that
      rather than pretending the current session picked it up.

  Tested on Windows (PowerShell 7.6.3, Win32NT): dry run, real install,
  the generated cadre.cmd shim, a real `cadre select` returning a dispatch
  plan, and a -Runner-scoped uninstall.

  Running this as a *file* needs -ExecutionPolicy Bypass, because a
  downloaded or UNC-hosted script is unsigned and blocked by default. The
  documented `irm ... | iex` invocation is unaffected -- Invoke-Expression
  never touches execution policy.

.EXAMPLE
  irm https://raw.githubusercontent.com/deagy/cadre/main/install.ps1 | iex

.EXAMPLE
  .\install.ps1 -Runner claude,codex -WithLifecycle
#>

[CmdletBinding()]
param(
    [ValidateSet('claude', 'codex', 'cline')]
    [string[]] $Runner,
    [switch] $WithLifecycle,
    [switch] $DryRun,
    [switch] $Uninstall
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$RepoSlug    = 'deagy/cadre'
$RepoUrl     = 'https://github.com/deagy/cadre.git'
$Marketplace = 'cadre-team'
$PluginName  = 'cadre'
$CacheDir    = if ($env:CADRE_HOME)    { $env:CADRE_HOME }    else { Join-Path $HOME '.cadre' }
$Checkout    = Join-Path $CacheDir 'dist'
$BinDir      = if ($env:CADRE_BIN_DIR) { $env:CADRE_BIN_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\cadre\bin' }
$CodexConfig = Join-Path $HOME '.codex\config.toml'
$BlockBegin  = '# >>> cadre >>>'
$BlockEnd    = '# <<< cadre <<<'

function Write-Step { param([string] $Message) Write-Host $Message }
function Write-Note { param([string] $Message) Write-Warning $Message }

# Every mutating action goes through this, so -DryRun is honest by
# construction rather than by remembering to check the flag at each site.
function Invoke-Step {
    param([string] $Description, [scriptblock] $Action)
    if ($DryRun) { Write-Host "  would run: $Description"; return }
    & $Action
}

function Test-Prerequisites {
    $missing = @()
    if (-not (Get-Command git -ErrorAction SilentlyContinue)) { $missing += 'git' }
    if ($missing.Count -gt 0) {
        throw "cadre-install: missing prerequisite(s): $($missing -join ', ')"
    }
}

function Get-DetectedRunners {
    @('claude', 'codex', 'cline') | Where-Object { Get-Command $_ -ErrorAction SilentlyContinue }
}

function Sync-Checkout {
    if (Test-Path (Join-Path $Checkout '.git')) {
        Write-Step "  updating $Checkout"
        Invoke-Step "git -C $Checkout fetch --depth 1 origin main" {
            git -C $Checkout fetch --quiet --depth 1 origin main
            git -C $Checkout reset --quiet --hard FETCH_HEAD
        }
    }
    else {
        Write-Step "  cloning into $Checkout"
        Invoke-Step "git clone --depth 1 $RepoUrl $Checkout" {
            New-Item -ItemType Directory -Force -Path $CacheDir | Out-Null
            git clone --quiet --depth 1 $RepoUrl $Checkout
        }
    }
}

function Install-Launcher {
    # A .cmd shim, not a symlink: symlinks on Windows require Developer Mode
    # or elevation, and an installer should need neither.
    $shim = Join-Path $BinDir 'cadre.cmd'
    $body = "@echo off`r`npowershell -NoProfile -ExecutionPolicy Bypass -File `"$Checkout\bin\cadre.ps1`" %*`r`n"
    Invoke-Step "write $shim" {
        New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
        Set-Content -Path $shim -Value $body -Encoding ASCII
    }
    # Past tense only when it actually happened.
    if (-not $DryRun) { Write-Step "  wrote $shim" }

    $userPath = [Environment]::GetEnvironmentVariable('PATH', 'User')
    if ($userPath -notlike "*$BinDir*") {
        Invoke-Step "add $BinDir to the user PATH" {
            [Environment]::SetEnvironmentVariable('PATH', "$BinDir;$userPath", 'User')
        }
        # Deliberately explicit: the registry-backed user PATH does not reach
        # an already-running shell, and silently "succeeding" here would look
        # like a broken install when `cadre` is not found.
        if ($DryRun) {
            Write-Step "  would add $BinDir to the user PATH (needs a new terminal)"
        }
        else {
            Write-Note "  Added $BinDir to your user PATH. Open a NEW terminal for it to take effect."
        }
    }
}

function Install-Claude {
    Write-Step 'claude:'
    Invoke-Step "claude plugin marketplace add $RepoSlug" { claude plugin marketplace add $RepoSlug }
    Invoke-Step "claude plugin install $PluginName@$Marketplace" {
        claude plugin install "$PluginName@$Marketplace" --scope user
    }
    if ($WithLifecycle) {
        Invoke-Step "claude plugin install cadre-lifecycle-core@$Marketplace" {
            claude plugin install "cadre-lifecycle-core@$Marketplace" --scope user
        }
    }
}

function Install-Codex {
    Write-Step 'codex:'
    Invoke-Step "codex plugin marketplace add $RepoSlug" { codex plugin marketplace add $RepoSlug }
    # `marketplace add` does not refresh an already-configured marketplace,
    # so a re-run would keep serving the cached snapshot without this.
    Invoke-Step "codex plugin marketplace upgrade $Marketplace" {
        codex plugin marketplace upgrade $Marketplace 2>$null
    }
    Invoke-Step "codex plugin add $PluginName@$Marketplace" { codex plugin add "$PluginName@$Marketplace" }
    if ($WithLifecycle) {
        Invoke-Step "codex plugin add cadre-lifecycle-core@$Marketplace" {
            codex plugin add "cadre-lifecycle-core@$Marketplace"
        }
    }
    # Non-fatal: bootstrap-codex refuses to overwrite a namespaced wrapper it
    # does not own, which is expected on a machine that already has some.
    Invoke-Step 'cadre bootstrap-codex' {
        & (Join-Path $Checkout 'bin\cadre.ps1') bootstrap-codex
        if ($LASTEXITCODE -ne 0) {
            Write-Note '  some Codex role wrappers were left alone (already present, not installed by cadre).'
        }
    }
    Set-CodexMcp
}

function Set-CodexMcp {
    $entry = @"
$BlockBegin
[mcp_servers.cadre-dispatch]
command = "cadre"
args = ["mcp-dispatch-server"]
$BlockEnd
"@
    if ($DryRun) { Write-Host "  would add an [mcp_servers.cadre-dispatch] block to $CodexConfig"; return }

    New-Item -ItemType Directory -Force -Path (Split-Path $CodexConfig) | Out-Null
    if (-not (Test-Path $CodexConfig)) { New-Item -ItemType File -Path $CodexConfig | Out-Null }

    if ((Get-Content $CodexConfig -Raw -ErrorAction SilentlyContinue) -match [regex]::Escape($BlockBegin)) {
        Write-Step "  $CodexConfig already has the cadre block; leaving it alone"
        return
    }

    # Back up before touching a file the operator owns and may have edited.
    Copy-Item $CodexConfig "$CodexConfig.cadre-backup" -Force
    Add-Content -Path $CodexConfig -Value "`r`n$entry"
    Write-Step "  added the cadre MCP block to $CodexConfig (backup: $CodexConfig.cadre-backup)"
}

function Install-Cline {
    Write-Step 'cline:'
    $source = Join-Path $Checkout 'cline-plugins\cline'
    Invoke-Step "cline plugin install $source --force" {
        cline plugin install $source --force
        if ($LASTEXITCODE -ne 0) {
            # Known upstream defect as of cline CLI 3.0.46, not fixable here.
            Write-Note '  cline install failed. If the error mentions cyclic structures, that is a'
            Write-Note '  known cline CLI defect (3.0.46), not a problem with this plugin.'
        }
    }
}

function Install-Kernel {
    # Pre-warm the kernel the lifecycle shim resolves, by asking it its
    # version. The shim downloads and verifies the release it was generated
    # against and caches it, so this is the same path a first real call takes;
    # doing it here surfaces a failure while the operator is still watching.
    #
    # This used to run bootstrap_sdlc.py, which created a venv and
    # pip-installed the kernel. That kernel was Python and no longer exists in
    # this repository; the shim fetches the Go one.
    Write-Step 'lifecycle kernel:'
    $shim = Join-Path $Checkout 'plugin\plugins\lifecycle\bin\agentic-sdlc'
    Invoke-Step "$shim --version" {
        & $shim --version
    }
}

function Invoke-Uninstall {
    param([string[]] $Targets, [bool] $Scoped)
    Write-Step "Removing Cadre for: $($Targets -join ', ')"
    foreach ($runner in $Targets) {
        switch ($runner) {
            'claude' {
                Invoke-Step 'claude plugin uninstall' {
                    claude plugin uninstall "$PluginName@$Marketplace" 2>$null
                    claude plugin marketplace remove $Marketplace 2>$null
                }
            }
            'codex' {
                Invoke-Step 'codex plugin remove' {
                    codex plugin remove "$PluginName@$Marketplace" 2>$null
                    codex plugin marketplace remove $Marketplace 2>$null
                }
            }
            'cline' { Invoke-Step 'cline plugin uninstall cadre' { cline plugin uninstall cadre 2>$null } }
        }
    }

    if (($Targets -contains 'codex') -and (-not $DryRun) -and (Test-Path $CodexConfig)) {
        $kept = @(); $skip = $false
        foreach ($line in Get-Content $CodexConfig) {
            if ($line -eq $BlockBegin) { $skip = $true; continue }
            if ($line -eq $BlockEnd)   { $skip = $false; continue }
            if (-not $skip) { $kept += $line }
        }
        Set-Content -Path $CodexConfig -Value $kept
        Write-Step "  removed the cadre block from $CodexConfig"
    }

    # The checkout and the shim are shared across runners, so a
    # runner-scoped uninstall must leave them in place.
    if ($Scoped) {
        Write-Step "  keeping $Checkout and the cadre.cmd shim (shared; -Runner was given)"
    }
    else {
        Invoke-Step 'remove the checkout and shim' {
            Remove-Item -Force -ErrorAction SilentlyContinue (Join-Path $BinDir 'cadre.cmd')
            Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $CacheDir
        }
    }
    Write-Step 'Done.'
}

# --- main ---------------------------------------------------------------

Test-Prerequisites

$scoped  = $PSBoundParameters.ContainsKey('Runner') -and $Runner
$targets = if ($scoped) { $Runner } else { Get-DetectedRunners }

if ($Uninstall) { Invoke-Uninstall -Targets $targets -Scoped:$scoped; exit 0 }

if (-not $targets) {
    throw 'cadre-install: no supported runner found (claude, codex, or cline). Install one first, or pass -Runner.'
}

if ($DryRun) { Write-Step '(dry run: nothing will be changed)' }
Write-Step "Runners: $($targets -join ', ')"
Write-Step ''

Write-Step 'checkout:'
Sync-Checkout
Install-Launcher
Write-Step ''

foreach ($runner in $targets) {
    switch ($runner) {
        'claude' { Install-Claude }
        'codex'  { Install-Codex }
        'cline'  { Install-Cline }
    }
    Write-Step ''
}

if ($WithLifecycle) { Install-Kernel; Write-Step '' }

Write-Step 'Done.'
Write-Step ''
Write-Step '  cadre select --task "..." --files a.go --task-id T-1'
if ($WithLifecycle) {
    Write-Step '  cadre sdlc validate --root .'
}
else {
    Write-Step ''
    Write-Step 'Lifecycle governance (G1-G10 gates) is optional and not installed.'
    Write-Step 'Re-run with -WithLifecycle if you want it.'
}
