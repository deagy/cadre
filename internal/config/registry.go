package config

import (
	"sort"
	"sync"
)

// FIELDS is the field registry, mirroring settings.py's FIELDS dict.
//
// Trust scope is security-critical: fields marked ScopeGlobalOnly may never
// be set from the project-local file, because that file is untrusted,
// clone-able repository content and these fields select executables or
// exfiltration-sensitive endpoints. See each field's comment (carried over
// from settings.py) for the specific reasoning.
var FIELDS = map[string]FieldSpec{
	"gitlab.base_url": {
		Key: "gitlab.base_url", EnvVar: "GITLAB_BASE_URL",
		Scope: ScopeGlobalOnly, Kind: "gitlab_base_url", Required: true,
	},
	"gitlab.project_id": {
		// global_only, not project_or_global: GitLab write scope is
		// contained operationally by pointing GITLAB_BASE_URL *and*
		// GITLAB_DOCS_PROJECT_ID at one dedicated, docs-only project with a
		// least-privilege service token, since gitlab.go performs no
		// classification check itself. Letting an untrusted project-local
		// file set the destination project would let a cloned repo
		// redirect every evidence-comment/wiki write to a project of its
		// choosing, silently weakening that recorded control.
		Key: "gitlab.project_id", EnvVar: "GITLAB_DOCS_PROJECT_ID",
		Scope: ScopeGlobalOnly, Kind: "project_id", Required: true,
	},
	"gitlab.supports_work_item_hierarchy": {
		Key: "gitlab.supports_work_item_hierarchy", EnvVar: "GITLAB_SUPPORTS_WORK_ITEM_HIERARCHY",
		Scope: ScopeProjectOrGlobal, Kind: "tristate_bool",
		HasStaticDefault: true, StaticDefault: nil,
	},
	"runners.claude_bin": {
		Key: "runners.claude_bin", EnvVar: "SECURE_CLOUD_AGENTS_CLAUDE_BIN",
		Scope: ScopeGlobalOnly, Kind: "executable",
		HasStaticDefault: true, StaticDefault: "claude",
	},
	"runners.codex_bin": {
		Key: "runners.codex_bin", EnvVar: "SECURE_CLOUD_AGENTS_CODEX_BIN",
		Scope: ScopeGlobalOnly, Kind: "executable",
		HasStaticDefault: true, StaticDefault: "codex",
	},
	"runners.codex_profile": {
		// Layered by `codex exec --profile <name>` from
		// $CODEX_HOME/<name>.config.toml, where the operator declares
		// their own [model_providers.*] block. That file is Codex's to
		// own, not this repository's -- which is precisely why no
		// base_url or credential for the codex runner appears here.
		Key: "runners.codex_profile", EnvVar: "SECURE_CLOUD_AGENTS_CODEX_PROFILE",
		Scope: ScopeGlobalOnly, Kind: "identifier",
		HasStaticDefault: true, StaticDefault: nil,
	},
	"runners.local_model_opus": {
		Key: "runners.local_model_opus", EnvVar: "SECURE_CLOUD_AGENTS_LOCAL_MODEL_OPUS",
		Scope: ScopeGlobalOnly, Kind: "identifier",
		HasStaticDefault: true, StaticDefault: nil,
	},
	"runners.local_model_sonnet": {
		Key: "runners.local_model_sonnet", EnvVar: "SECURE_CLOUD_AGENTS_LOCAL_MODEL_SONNET",
		Scope: ScopeGlobalOnly, Kind: "identifier",
		HasStaticDefault: true, StaticDefault: nil,
	},
	"runners.local_model_haiku": {
		Key: "runners.local_model_haiku", EnvVar: "SECURE_CLOUD_AGENTS_LOCAL_MODEL_HAIKU",
		Scope: ScopeGlobalOnly, Kind: "identifier",
		HasStaticDefault: true, StaticDefault: nil,
	},
	"runners.forward_env": {
		// Narrow, explicit extension of the dispatch environment allowlist,
		// for the single case that genuinely needs it. Empty by default,
		// so the deny-by-default posture is unchanged for every operator
		// who does not opt in.
		Key: "runners.forward_env", EnvVar: "SECURE_CLOUD_AGENTS_FORWARD_ENV",
		Scope: ScopeGlobalOnly, Kind: "env_var_name_list",
		HasStaticDefault: true, StaticDefault: nil,
	},
	"runners.api_base_url": {
		Key: "runners.api_base_url", EnvVar: "SECURE_CLOUD_AGENTS_API_BASE_URL",
		Scope: ScopeGlobalOnly, Kind: "endpoint_url",
		HasStaticDefault: true, StaticDefault: nil,
	},
	"runners.api_key_env": {
		Key: "runners.api_key_env", EnvVar: "SECURE_CLOUD_AGENTS_API_KEY_ENV",
		Scope: ScopeGlobalOnly, Kind: "env_var_name",
		HasStaticDefault: true, StaticDefault: nil,
	},
	"runners.api_allow_writes": {
		// The api runner has no OS-level sandbox to delegate to, so its
		// write-capable path is opt-in and off by default.
		Key: "runners.api_allow_writes", EnvVar: "SECURE_CLOUD_AGENTS_API_ALLOW_WRITES",
		Scope: ScopeGlobalOnly, Kind: "tristate_bool",
		HasStaticDefault: true, StaticDefault: false,
	},
	"runners.api_command_allowlist": {
		// Empty by default, which means the api runner offers no
		// command-execution tool at all.
		Key: "runners.api_command_allowlist", EnvVar: "SECURE_CLOUD_AGENTS_API_COMMAND_ALLOWLIST",
		Scope: ScopeGlobalOnly, Kind: "command_allowlist",
		HasStaticDefault: true, StaticDefault: nil,
	},
	"agentic_sdlc.bin_path": {
		Key: "agentic_sdlc.bin_path", EnvVar: "AGENTIC_SDLC_BIN",
		Scope: ScopeGlobalOnly, Kind: "executable",
		ComputedDefault: func() (any, bool) {
			path, ok := lookupWhich("agentic-sdlc")
			return path, ok
		},
	},
	"knowledge_store.home": {
		Key: "knowledge_store.home", EnvVar: "KNOWLEDGE_STORE_HOME",
		Scope: ScopeGlobalOnly, Kind: "path",
		HasStaticDefault: true, StaticDefault: nil,
	},
	"context_store.home": {
		// Global-only for exactly the same reason as knowledge_store.home:
		// it picks where a database is read and written, and a
		// project-local file arrives with `git clone` and is editable by
		// anyone who can open a pull request.
		Key: "context_store.home", EnvVar: "CONTEXT_STORE_HOME",
		Scope: ScopeGlobalOnly, Kind: "path",
		HasStaticDefault: true, StaticDefault: nil,
	},
	"roster.root": {
		// Global-only for the strongest reason of any field here: this
		// setting selects the role *prose* an agent is handed as its
		// operating instructions. A project-local file arrives with `git
		// clone`, so allowing it to redirect the roster would let a cloned
		// repository choose what its own reviewers are told to do.
		//
		// Deviation from settings.py: the Python default is
		// Path(__file__).resolve().parents[2] -- this checkout's own
		// roster/ directory, computed from the settings module's own file
		// location. A compiled Go binary has no equivalent of __file__, so
		// this instead walks up from cwd to the nearest .git boundary
		// (platform.RepoRoot's algorithm) and appends "roster". Set
		// RosterRootComputer (in resolve.go) to override this for a
		// caller with a different repo-root discovery strategy.
		Key: "roster.root", EnvVar: "CADRE_ROSTER_ROOT",
		Scope: ScopeGlobalOnly, Kind: "path",
		ComputedDefault: func() (any, bool) {
			return computeRosterRoot()
		},
	},
}

// RosterRootComputer, if set, overrides roster.root's computed default.
// Defaults to a .git-boundary walk from cwd, appending "roster". Exists so
// a caller with a different repo-root discovery strategy (e.g. one that
// already resolved a RosterManifest elsewhere) can substitute it without
// this package needing to import that caller's package (avoiding an import
// cycle with internal/orchestration).
var (
	rosterRootComputerMu sync.RWMutex
	rosterRootComputer   func() (string, bool)
)

// SetRosterRootComputer overrides how roster.root's computed default is
// found. Pass nil to restore the default .git-boundary walk.
func SetRosterRootComputer(f func() (string, bool)) {
	rosterRootComputerMu.Lock()
	defer rosterRootComputerMu.Unlock()
	rosterRootComputer = f
}

func computeRosterRoot() (any, bool) {
	rosterRootComputerMu.RLock()
	f := rosterRootComputer
	rosterRootComputerMu.RUnlock()
	if f != nil {
		return f()
	}
	return defaultComputeRosterRoot()
}

// spec looks up a field by key, or returns an error for an unknown key.
func spec(key string) (FieldSpec, error) {
	s, ok := FIELDS[key]
	if !ok {
		return FieldSpec{}, settingsErrorf("unknown settings key: %q", key)
	}
	return s, nil
}

// KnownKeys returns every registered field key, sorted.
func KnownKeys() []string {
	keys := make([]string, 0, len(FIELDS))
	for k := range FIELDS {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
