# Cadre CLI Go Refactoring Architecture

**Document Status:** Approved by Human Authorization  
**Date:** 2026-08-13  
**Approval:** Daniel Eagy (Human Project Lead)  
**Scope:** Full CLI refactoring from Python 3.10+ to Go  
**Constraint:** Agentic SDLC kernel remains Python-based delegation

---

## Executive Summary

This document defines the comprehensive architectural strategy for refactoring the Cadre CLI from Python to Go while preserving backward compatibility, feature parity, and the existing SDLC delegation model. The design prioritizes:

1. **Single monolithic Go binary** architecture with subcommand packages
2. **Incremental porting** (high-value subcommands first, knowledge store stays Python longer)
3. **Compatibility layer** allowing coexistence of Go and Python implementations during transition
4. **Exact behavioral replication** of the current CLI including all edge cases
5. **Zero new external dependencies** beyond viper (already preferred per library-standards.yaml)

---

## 1. Go Module Layout & Structure

### 1.1 Proposed Directory Layout

```
cadre/
├── cmd/cadre/
│   └── main.go                    # Single entry point, routes to subcommands
├── internal/
│   ├── cli/
│   │   ├── dispatcher.go          # Core dispatcher logic (parallel to cadre.py)
│   │   ├── version.go             # Version detection (parallel to cli_version())
│   │   └── sdlc.go                # SDLC delegation logic
│   ├── config/
│   │   ├── resolver.go            # Settings resolution (parallel to settings.py)
│   │   ├── defaults.go            # Config schema and defaults
│   │   └── validation.go          # Security checks (global_only scope)
│   ├── subcommands/
│   │   ├── select/
│   │   │   ├── cmd.go             # select subcommand
│   │   │   └── cmd_test.go
│   │   ├── knowledge/
│   │   │   ├── cmd.go             # knowledge subcommand (initially shells to Python)
│   │   │   └── cmd_test.go
│   │   ├── context/
│   │   │   ├── cmd.go
│   │   │   └── cmd_test.go
│   │   ├── config/
│   │   │   ├── cmd.go
│   │   │   └── cmd_test.go
│   │   ├── doctor/
│   │   │   ├── cmd.go
│   │   │   └── cmd_test.go
│   │   ├── upgrade/
│   │   │   ├── cmd.go
│   │   │   └── cmd_test.go
│   │   └── generate/ (all generate-* subcommands)
│   │       ├── plugin.go
│   │       ├── metadata.go
│   │       ├── aides.go
│   │       └── *_test.go
│   ├── interop/
│   │   └── python.go              # Fallback mechanism to invoke Python subcommands
│   └── platform/
│       ├── paths.go               # Path resolution (repo root, bin dir, etc.)
│       └── version.go             # Version marker parsing
├── go.mod
├── go.sum
├── .golangci.yml                  # Committed linting config (library-standards.yaml)
└── Makefile                       # Build targets
```

### 1.2 Rationale: Single Monolithic Binary

**Decision:** Single binary with subcommand packages, not multiple binaries.

**Reasoning:**

- **Dispatcher is thin:** The Python dispatcher is 280 lines; a Go equivalent is similarly constrained
- **Subcommands are logically cohesive:** All implement the Cadre CLI contract (subcommands.tsv rows)
- **Distribution simplification:** One artifact to build, sign, publish, and update
- **Version alignment:** All subcommands ship with identical versions; no cross-version compatibility testing needed
- **Testing efficiency:** Single binary CI build vs N binary builds
- **User experience:** `cadre upgrade` updates one binary, not N separate tools

**Comparison to alternatives:**

| Approach | Pros | Cons |
|----------|------|------|
| Single binary | Simple distribution, consistent versioning, one build artifact | Larger binary size (mitigated by selective linking) |
| Multiple binaries | Smaller per-tool artifact | Complex upgrade, version mismatches, N CI builds |
| Hybrid (Go dispatcher + Python libs) | Faster migration | Adds Python runtime dependency, fragile handoff |

**Cross-Platform Deployment:**

- **Development:** `go build` produces native binary
- **Distribution:** `pip install cadre` installs Go binary via entry point script (pyproject.toml) or embeds binary in wheel
- **Alternative:** Pure Go distribution (no Python package) if pip/pipx is dropped; this is a follow-up decision

---

## 2. Subcommand Implementation Strategy

### 2.1 Porting Priority & Phasing

**Phase 1 (Core dispatcher and lightweight commands):** Implement in Go first
- `cadre --version` (version detection from AST → file-based marker)
- `cadre help` and usage text
- `cadre config` (show/path/resolve settings)
- `cadre doctor` (diagnose installation, check version)
- `cadre upgrade` (check PyPI, detect install method, trigger update)
- `cadre sdlc` delegation (thin wrapper around AGENTIC_SDLC_BIN)

**Phase 2 (High-value orchestration commands):** Port after Phase 1 stabilizes
- `cadre select` (deterministic agent/gate selection)
- `cadre selection-telemetry` (opt-in telemetry summarization)
- `cadre profile` (provider/profile drift report)

**Phase 3 (Foundational generators):** Port only after Phase 1+2 are stable
- `cadre generate-role-metadata` (catalog + routing)
- `cadre generate-plugin` (package generation — most complex)
- `cadre generate-authority-aides` (authority role generation)

**Intentionally stays Python (for now):**
- `cadre knowledge` (vectorized knowledge store: SQLite + embeddings)
- `cadre context` (context store: embeddings + retrieval)
- `cadre init` (shared-policy overlay questionnaire — complex, infrequent)
- `cadre bootstrap-codex` (namespaced Codex wrapper installation)
- `cadre mcp-dispatch-server` (Codex MCP dispatch server)
- `cadre gitlab-evidence` (GitLab-specific tooling)
- `cadre role-fidelity` (model fidelity probing — complex LLM interaction)

### 2.2 Calling Python from Go (Fallback & Interop)

**Three paths for subcommand execution:**

1. **Native Go implementation:** Direct execution, fastest path
2. **Fallback to Python wrapper:** Subcommand invokes a Python script in `roster/*/src/`
3. **SDLC delegation:** Special case for `cadre sdlc`, never reimplemented

**Interop mechanism (`internal/interop/python.go`):**

```go
// PythonSubcommand invokes a Python subcommand from bin/cadre.py by
// importing the corresponding module from roster/*/src/.
// Returns the exit code; cmd.Run() errors are internal to the wrapper.
func PythonSubcommand(ctx context.Context, script string, args []string) (int, error) {
    // Replicate bin/cadre.py's subprocess.run logic:
    // 1. Resolve repository root (REPOSITORY_ROOT in select_agents.py)
    // 2. Locate Python 3.10+ (bin/cadre shell logic)
    // 3. Construct [sys.executable, <repo>/script, *args]
    // 4. Pass CADRE_INTERACTIVE if --interactive flag was set
    // 5. Return exit code
}
```

**Dispatch table evolution:**

During migration, `bin/cadre.py` is kept but modified:

```python
# In bin/cadre.py (modified during transition)
def dispatch_subcommand(name, script, rest, interactive=False):
    # If the Go binary supports this subcommand, delegate to it
    if has_go_implementation(name):
        return subprocess.run(
            [os.environ.get("CADRE_GO_BIN", "./cadre"), name, *rest],
            env=_child_env(interactive)
        ).returncode
    
    # Otherwise, fall back to Python
    return subprocess.run(
        [sys.executable, script, *rest],
        env=_child_env(interactive)
    ).returncode
```

This allows the Go binary to be "plugged in" and tested without rewriting Python.

### 2.3 CLI Framework Choice

**Decision:** Go's standard `flag` package + custom dispatcher, not third-party frameworks.

**Reasoning:**

- **Minimal dependencies:** Aligns with library-standards.yaml's `prefer_standard_library_when_sufficient`
- **Exact compatibility:** Easier to match Python argparse behavior (order of --help, unknown flag handling, etc.)
- **Subcommand-specific parsing:** Each subcommand defines its own `flag.FlagSet`, avoiding global state (parallels viper constraint in library-standards.yaml)

**Alternative considered & rejected:**

- **cobra:** Popular, but adds transitive deps (spf13/pflag, others); overkill for 18 subcommands with simple args
- **urfave/cli:** Simpler than cobra, but still adds dependencies; less native to stdlib

**Subcommand argument parsing template:**

```go
// internal/subcommands/select/cmd.go
func NewCmd() *Command {
    cmd := &Command{
        name:        "select",
        description: "Deterministic agent/gate selection",
        fs:          flag.NewFlagSet("select", flag.ContinueOnError),
    }
    cmd.fs.StringVar(&cmd.task, "task", "", "Task objective (required)")
    cmd.fs.StringVar(&cmd.root, "root", "", "Repository root (default: cwd or parent)")
    // ... more flags
    cmd.fs.Usage = func() { cmd.Usage() }
    return cmd
}

func (c *Command) Run(ctx context.Context, args []string) error {
    if err := c.fs.Parse(args); err != nil {
        return fmt.Errorf("parse error: %w", err)
    }
    if c.task == "" {
        return fmt.Errorf("--task is required")
    }
    // Real implementation
    return c.run(ctx)
}
```

### 2.4 Backward Compatibility Guarantees

**Invariants maintained:**

1. **Argument order:** `cadre [--interactive] <subcommand> [args...]` structure unchanged
2. **Exit codes:** 0 = success, 1 = error, 2 = argument parse error (matching Python convention)
3. **Output format:** JSON, YAML, text formats bit-for-bit identical where feasible
4. **Error messages:** Where Python messages are user-facing, Go versions must be identical (prevents script breakage)
5. **Stdin/stdout/stderr:** Same redirection behavior, same handling of TTY detection for `--interactive`
6. **Environment variables:** All existing CADRE_* and subprocess env vars respected
7. **Config file parsing:** Exact YAML/JSON parsing with same error messages for malformed files
8. **Provider injection logic:** Identical to `_resolve_provider_injection()` in bin/cadre.py

**Test matrix for compatibility:**

- Argument combinations (cross-product of flags)
- Environment variable precedence (env > config file > default)
- Error conditions (missing files, invalid JSON, etc.)
- Output format (JSON pretty-printing, YAML quoting, etc.)
- TTY detection and `--interactive` flag interaction

---

## 3. Settings/Config Resolution in Go

### 3.1 Precedence Chain (Replicate settings.py Exactly)

```
1. Environment variable (e.g., CADRE_ROSTER_ROOT)
2. Project-local .agents/cadre.yaml (or .json)
3. User-global ~/.config/cadre/config.yaml (or ~/.config/cadre/config.json)
4. Static default (hardcoded in code)
5. Computed default (e.g., $HOME/something)
6. Interactive prompt (only if CADRE_INTERACTIVE=1 and stdin/stdout are TTYs)
7. Error (field is required and no value found)
```

### 3.2 Config Schema & Defaults

**Settings structure (mirrors settings.py registrations):**

```go
// internal/config/schema.go
type Config struct {
    // Scope: project_or_global
    Roster struct {
        Root string `yaml:"root"`  // ~/.config/cadre/config.yaml
    } `yaml:"roster"`

    // Scope: global_only (project-local file cannot set these)
    AgenticSDLC struct {
        BinPath string `yaml:"bin_path"`
    } `yaml:"agentic_sdlc"`

    GitLab struct {
        Token string `yaml:"token"`  // NEVER serialized; only read from env
    } `yaml:"gitlab"`

    KnowledgeStore struct {
        Root string `yaml:"root"`
    } `yaml:"knowledge_store"`
    
    ContextStore struct {
        Root string `yaml:"root"`
    } `yaml:"context_store"`

    // Other fields...
}

// Global defaults
var Defaults = Config{
    Roster: RosterConfig{
        Root: "~/.config/cadre/rosters/default",
    },
    // ...
}
```

### 3.3 Implementation: `internal/config/resolver.go`

```go
// ResolveString resolves a dotted key (e.g., "agentic_sdlc.bin_path")
// following the precedence chain above.
func ResolveString(ctx context.Context, key string) (string, error) {
    // 1. Check environment variable (CADRE_<KEY_UPPER_SNAKE>)
    envKey := "CADRE_" + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
    if val := os.Getenv(envKey); val != "" {
        return val, nil
    }

    // 2. Load and check project-local config
    if projectCfg, err := loadProjectConfig(); err == nil {
        if val := getConfigValue(projectCfg, key); val != "" {
            // Security check: if key is in global_only scope, error
            if scope := configFieldScope(key); scope == "global_only" {
                return "", SettingsScopeError{
                    Key: key,
                    File: projectConfigPath(),
                }
            }
            return val, nil
        }
    }

    // 3. Load and check user-global config
    if globalCfg, err := loadGlobalConfig(); err == nil {
        if val := getConfigValue(globalCfg, key); val != "" {
            return val, nil
        }
    }

    // 4. Return static/computed default
    if val := getDefault(key); val != "" {
        return val, nil
    }

    // 5. Interactive prompt (if CADRE_INTERACTIVE=1 and TTY)
    if os.Getenv("CADRE_INTERACTIVE") == "1" && isTTY(os.Stdin) {
        return promptAndSave(ctx, key)
    }

    // 6. Error
    return "", ErrSettingNotFound{Key: key}
}

// Validate ensures project-local config doesn't set global_only fields
func Validate(cfg *Config, filePath string) error {
    // Walk cfg, check scope annotations
    // Return SettingsScopeError if violation found
}
```

### 3.4 Config File Discovery & Parsing

**Project-local config discovery** (parallel to `find_file_at_project_root` in roster/shared/src/resolve.py):

```go
func findProjectConfig() (string, error) {
    // Walk from cwd to nearest .git boundary
    // Look for .agents/cadre.yaml or .agents/cadre.json
    // Return path to first found, or error if both exist (ambiguous)
    // Maximum walk depth: 10 (MAXIMUM_WALK_DEPTH from resolve.py)
}
```

**User-global config discovery:**

```go
func globalConfigPath() (string, error) {
    // ${XDG_CONFIG_HOME:-~/.config}/cadre/config.yaml (or .json)
    // Replaces configdir library; use standard os/user + filepath
}
```

**Parsing:**

- Use `github.com/spf13/viper` (already preferred in library-standards.yaml)
- Viper handles YAML/JSON polymorphism
- Manual secret-key rejection (parallel to `_looks_like_secret_key` in settings.py)

### 3.5 Security Constraints

**Global-only scope:**

Fields marked `global_only` can only be set via:
- Environment variables (CADRE_*, always trusted)
- User-global config file (user controls, trusted)
- Static/computed defaults (hardcoded, trusted)

**Never allowed in:**
- Project-local `.agents/cadre.yaml` (untrusted, clonable content)

**Violation handling:** Raise `SettingsScopeError` immediately, never silently ignore.

**Fields with global_only scope:**
- `agentic_sdlc.bin_path` (selects executable; security event)
- `knowledge_store.root` (selects data storage location)
- `context_store.root` (selects data storage location)

---

## 4. Cross-Platform Considerations

### 4.1 Single Go Binary for All Platforms

**Decision:** Compile once per target platform (linux/amd64, darwin/arm64, windows/amd64, etc.), distribute a Go binary directly.

**Advantages over Python shebang approach:**

| Concern | Python Approach | Go Approach |
|---------|-----------------|-----------|
| Python version detection | Complex shell/PowerShell logic in bin/cadre, bin/cadre.ps1 | Not needed; Go runtime is self-contained |
| Binary distribution | Wheel with Python package + shell wrapper | Single static binary |
| Platform differences | Multiple wrapper files (sh, ps1) | Single build, conditional compilation for OS-specific code (path separators, etc.) |
| Cold start | Python interpreter startup + import overhead | Minimal (static binary, no JIT) |
| Installation | `pip install cadre` (requires Python) | Direct `cadre` binary on PATH (or `brew install cadre`, etc.) |

### 4.2 Platform-Specific Implementation Details

**File paths & separators:**

```go
// internal/platform/paths.go
func RepoRoot() (string, error) {
    // Walk from cwd to nearest `.git/` or `.git` file (symlink for worktrees)
    // Use filepath.IsDir() to detect .git/ vs .git file
    // Return absolute path
}

func FindProjectRoot(from string) (string, error) {
    // Same logic, but from a given directory
    // Used by config discovery to find `.agents/cadre.yaml`
}
```

**Conditional code (e.g., XDG Base Directory on Unix, APPDATA on Windows):**

```go
// +build !windows
func globalConfigDir() string {
    if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
        return xdg
    }
    return filepath.Join(os.Getenv("HOME"), ".config")
}

// +build windows
func globalConfigDir() string {
    if appdata := os.Getenv("APPDATA"); appdata != "" {
        return appdata
    }
    return filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Roaming")
}
```

### 4.3 Distribution Strategy

**Immediate (during Go CLI development):**
- Keep `pip install cadre` as the primary distribution
- Python wheel contains embedded Go binary (or symlink to a separate download)
- `bin/cadre` shell wrapper is replaced with a thin shim that invokes the Go binary

**Alternative (longer-term, decision deferred):**
- Pure Go distribution: `cadre` binary via Homebrew, apt, etc.
- Separate from PyPI; would require separate upgrade/installation docs
- Requires explicit approval due to distribution impact

**Build & release process:**

```makefile
# Makefile (sketch)
build:
	@mkdir -p bin
	GOOS=linux GOARCH=amd64 go build -o bin/cadre-linux-amd64 ./cmd/cadre
	GOOS=darwin GOARCH=arm64 go build -o bin/cadre-darwin-arm64 ./cmd/cadre
	GOOS=windows GOARCH=amd64 go build -o bin/cadre-windows-amd64.exe ./cmd/cadre
	# Signed binaries, SBOMs, checksums handled by CI/release pipeline

test:
	go test -race ./...
	go vet ./...

fmt:
	gofmt -w .
	go run golang.org/x/tools/cmd/goimports@latest -w .

lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run ./...
```

### 4.4 No Python Runtime Dependency (Long-term)

The Go binary itself has **no runtime Python dependency**, BUT:
- Subcommands that stay Python (knowledge, context, etc.) still require Python 3.10+
- For those subcommands, the Go binary will shell out to Python (through `internal/interop/python.go`)
- This is acceptable because it's a controlled fallback, not a global constraint

---

## 5. SDLC Delegation Strategy

### 5.1 Current Behavior (Python)

```python
# bin/cadre.py: dispatch_sdlc()
def dispatch_sdlc(rest, interactive=False):
    sdlc_bin = resolve_optional("agentic_sdlc.bin_path", env=...)
    if not sdlc_bin:
        in_tree = REPO_ROOT / "bin" / "agentic-sdlc"
        if in_tree.is_file():
            sdlc_bin = str(in_tree)
    if not sdlc_bin:
        # Error: SDLC not found
        return 1

    rest, suppress_default = _resolve_provider_injection(rest)
    provider_args = []
    if not suppress_default:
        provider_args = ["--provider", str(REPO_ROOT / "provider" / "provider.json")]

    result = subprocess.run(
        [sdlc_bin, *provider_args, *rest],
        env=_child_env(interactive)
    )
    return result.returncode
```

### 5.2 Go Implementation (Nearly Identical)

```go
// internal/cli/sdlc.go
func DispatchSDLC(ctx context.Context, rest []string, interactive bool) (int, error) {
    // 1. Resolve AGENTIC_SDLC_BIN setting
    sdlcBin, err := config.ResolveString(ctx, "agentic_sdlc.bin_path")
    if err != nil && err != config.ErrSettingNotFound {
        return 1, err
    }

    // 2. Fallback to in-tree kernel
    if sdlcBin == "" {
        inTree := filepath.Join(repoRoot, "bin", "agentic-sdlc")
        if info, err := os.Stat(inTree); err == nil && !info.IsDir() {
            sdlcBin = inTree
        }
    }

    if sdlcBin == "" {
        fmt.Fprintf(os.Stderr, sdlcInstallMessage())
        return 1, nil
    }

    // 3. Provider injection (exact replica of _resolve_provider_injection)
    rest, suppressDefault := resolveProviderInjection(rest)
    providerArgs := []string{}
    if !suppressDefault {
        providerArgs = []string{
            "--provider",
            filepath.Join(repoRoot, "provider", "provider.json"),
        }
    }

    // 4. Execute with subprocess (mirrors subprocess.run)
    cmd := exec.CommandContext(ctx, sdlcBin, append(providerArgs, rest...)...)
    if interactive {
        cmd.Env = append(os.Environ(), "CADRE_INTERACTIVE=1")
    } else {
        cmd.Env = os.Environ()
    }
    cmd.Stdin = os.Stdin
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr

    err = cmd.Run()
    return cmd.ProcessState.ExitCode(), err
}

// resolveProviderInjection exactly mirrors _resolve_provider_injection
func resolveProviderInjection(rest []string) ([]string, bool) {
    // Parse with argparse-equivalent logic
    // Return (forwarded args, suppress_default bool)
}
```

### 5.3 Provider Injection Logic (Exact Replica)

The tricky part: reproducing argparse's `--provider` detection to avoid suppressing injections when the user supplies their own.

**Implementation strategy:**

```go
func resolveProviderInjection(rest []string) ([]string, bool) {
    // Custom argparse for the subset we care about:
    // - --no-default-provider (store_true) → suppress
    // - --provider (action="append") → suppress if present
    
    // Can't use flag.FlagSet because we need to see unknown flags and preserve order
    // Manual parsing:
    
    var noDefaultProvider bool
    var providerSupplied []string
    var remainingArgs []string
    
    for i := 0; i < len(rest); i++ {
        arg := rest[i]
        
        if arg == "--no-default-provider" {
            noDefaultProvider = true
            continue
        }
        
        if arg == "--provider" {
            if i+1 < len(rest) {
                providerSupplied = append(providerSupplied, rest[i+1])
                i++ // skip the value
            }
            continue
        }
        
        if strings.HasPrefix(arg, "--provider=") {
            val := strings.TrimPrefix(arg, "--provider=")
            providerSupplied = append(providerSupplied, val)
            continue
        }
        
        remainingArgs = append(remainingArgs, arg)
    }
    
    // Reconstruct: forwarded args with --provider values in order
    var forwarded []string
    for _, provider := range providerSupplied {
        forwarded = append(forwarded, "--provider", provider)
    }
    forwarded = append(forwarded, remainingArgs...)
    
    suppress := noDefaultProvider || len(providerSupplied) > 0
    return forwarded, suppress
}
```

### 5.4 Testing Provider Injection

**Test vectors (from cadre.py docstring):**

```go
func TestProviderInjection(t *testing.T) {
    tests := []struct{
        input    []string
        want     []string
        suppress bool
    }{
        // Case 1: No --provider → don't suppress (inject Cadre's)
        {
            input:    []string{"plan", "--task", "foo"},
            want:     []string{"plan", "--task", "foo"},
            suppress: false,
        },
        // Case 2: --provider X → suppress
        {
            input:    []string{"--provider", "/path/to/other", "plan"},
            want:     []string{"--provider", "/path/to/other", "plan"},
            suppress: true,
        },
        // Case 3: --provider=X → suppress
        {
            input:    []string{"--provider=/path/to/other", "plan"},
            want:     []string{"--provider=/path/to/other", "plan"},
            suppress: true,
        },
        // Case 4: --no-default-provider → suppress
        {
            input:    []string{"--no-default-provider", "plan"},
            want:     []string{"plan"},
            suppress: true,
        },
        // Case 5: Multiple --provider → all preserved, suppress
        {
            input:    []string{"--provider", "a", "--provider", "b", "plan"},
            want:     []string{"--provider", "a", "--provider", "b", "plan"},
            suppress: true,
        },
    }
    
    for _, tt := range tests {
        forwarded, suppress := resolveProviderInjection(tt.input)
        if suppress != tt.suppress || !slices.Equal(forwarded, tt.want) {
            t.Errorf("input %v: got %v/%v, want %v/%v", 
                tt.input, forwarded, suppress, tt.want, tt.suppress)
        }
    }
}
```

---

## 6. Testing & Validation Strategy

### 6.1 Test Architecture

**Three levels of testing:**

1. **Unit tests** (test each subcommand in isolation)
2. **Integration tests** (test full CLI dispatch, config resolution)
3. **Compatibility tests** (compare Go output to Python reference)

**Test layout:**

```
cadre/
├── internal/
│   ├── cli/
│   │   ├── dispatcher_test.go         # Route parsing, version detection
│   │   ├── sdlc_test.go               # SDLC delegation, provider injection
│   │   └── ...
│   ├── config/
│   │   ├── resolver_test.go           # Precedence chain, env var handling
│   │   ├── security_test.go           # global_only scope violations
│   │   └── ...
│   ├── subcommands/
│   │   ├── select/
│   │   │   ├── cmd_test.go            # Argument parsing, basic dispatch
│   │   │   └── testdata/
│   │   │       ├── input_*.json       # Golden file inputs
│   │   │       └── expected_*.json    # Golden file outputs
│   │   └── ...
│   └── platform/
│       ├── paths_test.go              # Repo root detection, path walking
│       └── ...
├── testdata/
│   ├── configs/
│   │   ├── project_cadre.yaml         # Test config files
│   │   ├── global_cadre.yaml
│   │   └── malformed.json             # Error cases
│   └── repos/
│       ├── with_git/
│       ├── nested/
│       └── ...
└── test/
    └── compatibility_test.go          # Go vs Python output comparison
```

### 6.2 Unit Tests by Domain

**CLI dispatcher tests (`internal/cli/dispatcher_test.go`):**

```go
func TestDispatcher_RouteSubcommand(t *testing.T) {
    tests := []struct {
        argv    []string
        wantCmd string
        wantRest []string
    }{
        {[]string{"select", "--task", "foo"}, "select", []string{"--task", "foo"}},
        {[]string{"--interactive", "config", "show"}, "config", []string{"show"}},
        {[]string{}, "help", []string{}},
        {[]string{"unknown"}, "", nil}, // Error case
    }
    // ... test each case
}

func TestDispatcher_VersionDetection(t *testing.T) {
    // Mock version file at several locations:
    // - cadre_cli/_version.py (pip/pipx install)
    // - ../cadre_cli/_version.py (source checkout)
    // Test parsing of VERSION = "0.23.2" constant
}
```

**Config resolution tests (`internal/config/resolver_test.go`):**

```go
func TestResolve_Precedence(t *testing.T) {
    // Env > project-local > global > default
    // Set env var, run config.Resolve, verify takes env value
    
    // Clear env, set project-local, verify takes project value
    // (but skip if global_only scope)
    
    // Clear both, set global, verify takes global
    
    // Clear all, verify returns default or error
}

func TestResolve_GlobalOnlyViolation(t *testing.T) {
    // Create .agents/cadre.yaml that sets agentic_sdlc.bin_path
    // Call Validate(); expect SettingsScopeError
}

func TestResolve_TTYDetection(t *testing.T) {
    // Set CADRE_INTERACTIVE=1 with mocked stdin (TTY)
    // Should prompt (test with mock input)
    
    // Set CADRE_INTERACTIVE=1 with non-TTY stdin
    // Should not prompt, should error or return default
}
```

**Platform path tests (`internal/platform/paths_test.go`):**

```go
func TestFindProjectRoot(t *testing.T) {
    // Create a temp directory structure:
    // tmp/
    //   .git/
    //   subdir/
    //     nested/
    
    // Call FindProjectRoot from nested; expect to find tmp/
    // Call from a directory with no .git; expect error
    
    // Test .git as symlink (worktree case)
}
```

### 6.3 Integration Tests

**End-to-end subcommand execution (`test/integration_test.go`):**

```go
func TestIntegration_SelectCommand(t *testing.T) {
    // Create temp repo with .agents/cadre.yaml
    // Run: cadre select --task "create a test"
    // Capture output (JSON)
    // Validate structure: plan, agents, gates, etc.
}

func TestIntegration_ConfigCommand(t *testing.T) {
    // Run: cadre config show
    // Expect YAML output with resolved settings
    
    // Run: cadre config resolve roster.root
    // Expect single value printed
}
```

### 6.4 Compatibility Tests (Go vs Python)

**Golden file approach:**

1. Run Python CLI on test inputs, save outputs to golden files
2. Run Go CLI on same inputs
3. Compare JSON/YAML structures (not byte-for-byte, but semantically)

```go
func TestCompatibility_SelectOutput(t *testing.T) {
    // Load golden file: testdata/select_output.json (from Python)
    golden := loadGoldenJSON("testdata/select_output.json")
    
    // Run Go binary: cadre select --task "..."
    var output map[string]interface{}
    runCLI(t, "select", "--task", "...", &output)
    
    // Compare: same agents, same gates, same plan structure
    if !deepEqual(output, golden) {
        t.Errorf("output differs from Python reference")
    }
}
```

### 6.5 Edge Cases & Regression Tests

**Must-test scenarios:**

- Arguments with spaces: `cadre select --task "multi word task"`
- Quoted JSON in config: YAML parsing edge cases
- Circular symlinks in .git discovery
- Missing vs empty CADRE_* env vars
- Config file permission errors (read-only, permission denied)
- Deeply nested .agents/cadre.yaml (path containment)
- SDLC binary not found, in-tree fallback, env var override
- Provider injection with multiple --provider flags
- Interactive prompt: verify env var passed to subcommand
- Version detection from both cadre_cli/_version.py and ../cadre_cli/_version.py paths

---

## 7. Migration Path: Incremental or Big-Bang?

### 7.1 Recommended: Incremental Phasing with Compatibility Layer

**Migration strategy: Three phases with coexistence**

**Phase 1 (Weeks 1-2): Core infrastructure in Go**
- Implement dispatcher, version detection, settings resolver
- Implement config, doctor, upgrade subcommands
- Implement SDLC delegation
- Create interop layer to call Python subcommands
- Tests pass; Go binary can run any subcommand (via fallback to Python)
- **No breaking changes** — bin/cadre.py still works; Go binary is opt-in

**Phase 2 (Weeks 3-4): Port high-value commands**
- Port `cadre select` to Go
- Port `cadre selection-telemetry` to Go
- Port `cadre profile` to Go
- Compatibility tests verify output matches Python reference
- Subcommand implementations merged one at a time

**Phase 3 (Weeks 5-6): Complete core orchestration**
- Port `cadre generate-role-metadata`, `generate-plugin`, `generate-authority-aides`
- These are the most complex; stabilize Phase 1+2 first
- Defer knowledge/context stores (they're fine in Python)

**Switchover (Week 7):**
- bin/cadre becomes thin wrapper that always invokes Go binary
- bin/cadre.py kept as reference/fallback for any subcommands not yet ported
- Update CI to test Go binary path
- Gradual rollout: test with dogfooding before releasing

### 7.2 Compatibility Layer Details

**During phases 1-3, bin/cadre.py is modified to support two modes:**

```python
def main(argv):
    # Check if CADRE_USE_GO_BINARY is set or if Go binary is available
    go_binary = os.environ.get("CADRE_GO_BINARY")
    if not go_binary and shutil.which("cadre-go"):  # cadre-go is the compiled binary name during development
        go_binary = "cadre-go"
    
    if go_binary:
        # Try Go first for ported subcommands
        result = subprocess.run([go_binary] + argv, env=os.environ.copy())
        if result.returncode != 127:  # Not "command not found"
            return result.returncode
        # Fall back to Python if Go doesn't support this subcommand
    
    # Fall back to Python for any subcommand
    return dispatch_python(argv)
```

**In practice during development:**

- CI builds both Python and Go versions
- Tests run both versions independently
- Compatibility tests compare outputs
- bin/cadre tries Go first, falls back to Python
- User sees no difference; smooth transition

### 7.3 Why Not Big-Bang?

**Risks of rewriting everything at once:**

- **Parallel testing burden:** Can't compare Go output to Python reference if Python is replaced
- **Bug discovery latency:** Integration issues only surface after full rewrite
- **Review complexity:** 10k+ lines of Go in one PR is hard to review
- **Rollback cost:** If Go implementation has gaps, reverting is painful
- **Release risk:** One coordinated switch vs gradual rollout

**Incremental advantages:**

- Each subcommand ported can be independently tested/reviewed
- Fallback to Python for any unported subcommands (no blocker)
- Easier to spot integration issues early
- Smaller PRs (better review)
- Can release Go CLI without requiring all subcommands be ported

### 7.4 Timeline Estimates

| Phase | Tasks | Effort | Duration |
|-------|-------|--------|----------|
| 1 | Dispatcher, version, config, doctor, upgrade, SDLC, interop | 30 hrs | ~2 weeks |
| 2 | select, selection-telemetry, profile, compat tests | 20 hrs | ~2 weeks |
| 3 | generate-{plugin,metadata,aides}, compat tests | 25 hrs | ~2 weeks |
| Switchover | bin/cadre update, CI, release | 5 hrs | ~1 week |
| **Total** | | **80 hrs** | **~7 weeks** |

(Estimates assume one engineer working full-time on CLI refactoring.)

---

## 8. Dependencies & Constraints

### 8.1 Go Dependencies (Minimal)

**Required dependencies:**

| Dependency | Module | Justification | Precedent |
|------------|--------|---------------|-----------|
| Standard library | (stdlib) | gofmt, go vet, flag, os/exec, filepath, encoding/json, etc. | Required |
| viper | github.com/spf13/viper | Config parsing (YAML/JSON); preferred in library-standards.yaml | Yes |
| yaml.v3 | gopkg.in/yaml.v3 | YAML parsing (used by viper, or direct if needed) | Standard |

**Strongly avoid:**

- cobra (adds pflag + others; overkill for 18 subcommands)
- urfave/cli (cleaner than cobra, but still unnecessary for static subcommands)
- Any embedding/vector DB client (knowledge store stays Python)

**Test dependencies (only in _test.go):**

| Dependency | Justification |
|------------|---------------|
| testify/require, testify/assert | Standard; library-standards.yaml approved |
| stretchr/testify/mock | Mocking; library-standards.yaml approved |

### 8.2 Constraints & Non-Negotiables

**1. Backward compatibility:**
- Every flag, argument, exit code, output format must match Python CLI
- Error messages must be identical (scripts depend on them)
- Environment variable names unchanged

**2. Security:**
- global_only scope enforced; project-local file cannot set these
- No new secrets in config files; env vars only
- No hardcoded credentials or paths

**3. Platform support:**
- POSIX (Linux, macOS)
- Windows (cmd.exe, PowerShell)
- WSL and Docker containers

**4. SDLC delegation:**
- Agentic SDLC remains Python-based
- Go CLI just shells out to agentic-sdlc binary
- No reimplementation of lifecycle kernel

**5. Distribution:**
- Initial: embed Go binary in Python wheel (pip install cadre still works)
- Long-term: separate Go distribution (deferred decision)

**6. No new external dependencies without:**
- Documented technical rationale (library-standards.yaml rule)
- Architecture review (blocking decision)
- Security review (if introducing new providers, API calls, etc.)
- License review (GPL, AGPL, etc. not acceptable)

### 8.3 Python Dependencies (for Remaining Subcommands)

**Unchanged from current:**
- PyYAML (YAML parsing)
- sqlite3 (stdlib; knowledge store schema)
- requests (HTTP calls for upgrade check)
- Any LLM/embedding SDKs (knowledge store)

**These remain in bin/cadre.py and subcommand scripts even after Go port** because knowledge/context stores are not ported yet.

---

## 9. References & Related Documents

- **Current implementation:** `/home/deagy/sdk/cadre/bin/cadre.py` (dispatcher), `/home/deagy/sdk/cadre/bin/subcommands.tsv` (route table)
- **Config resolver (Python reference):** `roster/shared/src/settings.py`
- **Library standards:** `roster/shared/library-standards.yaml` (Go tool/library preferences)
- **Team profile:** `roster/shared/team-profile.yaml` (primary_language: golang)
- **Agent autonomy:** `roster/shared/agent-autonomy.yaml` (governance model)
- **Operating principles:** `roster/shared/operating-principles.md` (decision-making guide)
- **Related workspace:** `/home/deagy/sdk/pkg/` (Go monorepo with proven expertise)

---

**End of Architectural Design Document**
