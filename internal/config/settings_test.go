package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// isolateConfigEnv points the global config dir at a fresh temp directory
// and resets the file cache, so tests never touch the real operator's
// ~/.config/cadre or leak state between runs.
func isolateConfigEnv(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// Clear every field's environment variable for the duration of the test.
	//
	// The environment is tier 1: it wins over both the project file and the
	// global one, and resolveCore returns before either is read. So a test
	// asserting something about *file* resolution silently measures the
	// ambient environment instead whenever the matching variable happens to
	// be set.
	//
	// Not hypothetical. TestEveryGlobalOnlyFieldIsRefusedFromAProjectFile
	// failed on CI the moment the workflow started exporting
	// AGENTIC_SDLC_BIN: the project-local value it expected to be refused
	// was never reached, because the environment answered first. It failed
	// loudly rather than passing falsely, which is the good direction -- but
	// a guard whose verdict depends on the machine it runs on is not a guard.
	//
	// Unset rather than set-to-empty: an empty value is its own error here
	// ("%s is set but empty/whitespace-only"), so blanking would swap one
	// environment-dependent answer for another.
	for _, field := range FIELDS {
		name := field.EnvVar
		if name == "" {
			continue
		}
		previous, wasSet := os.LookupEnv(name)
		if !wasSet {
			continue
		}
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("cannot isolate %s: %v", name, err)
		}
		t.Cleanup(func() { _ = os.Setenv(name, previous) })
	}

	ResetCache()
	t.Cleanup(ResetCache)
}

func makeGitCheckout(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// --- Validators ---

func TestValidateGitLabBaseURL(t *testing.T) {
	s := FIELDS["gitlab.base_url"]
	if _, err := validate(s, "http://gitlab.example.com"); err == nil {
		t.Error("expected rejection of non-https")
	}
	if _, err := validate(s, "https://user@gitlab.example.com"); err == nil {
		t.Error("expected rejection of URL userinfo")
	}
	v, err := validate(s, "https://gitlab.example.com/")
	if err != nil || v != "https://gitlab.example.com" {
		t.Errorf("v=%v err=%v", v, err)
	}
}

func TestValidateExecutableRejectsRelativePath(t *testing.T) {
	s := FIELDS["runners.claude_bin"]
	if _, err := validate(s, "sub/dir/claude"); err == nil {
		t.Error("expected rejection of relative path with separator")
	}
	if _, err := validate(s, "claude"); err != nil {
		t.Errorf("bare executable name should be valid: %v", err)
	}
	if _, err := validate(s, "/usr/bin/claude"); err != nil {
		t.Errorf("absolute path should be valid: %v", err)
	}
}

func TestValidateExecutableRejectsLeadingDash(t *testing.T) {
	s := FIELDS["runners.claude_bin"]
	if _, err := validate(s, "-a"); err == nil {
		t.Error("expected rejection of a value beginning with '-'")
	}
}

func TestValidateExecutableRejectsControlCharacters(t *testing.T) {
	s := FIELDS["runners.claude_bin"]
	if _, err := validate(s, "claude\nbin"); err == nil {
		t.Error("expected rejection of embedded newline")
	}
}

func TestValidateIdentifier(t *testing.T) {
	s := FIELDS["runners.codex_profile"]
	if _, err := validate(s, "qwen3-coder:30b"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if _, err := validate(s, "org/model-name"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if _, err := validate(s, "has space"); err == nil {
		t.Error("expected rejection of whitespace")
	}
	if _, err := validate(s, "-leading-dash"); err == nil {
		t.Error("expected rejection of a leading dash")
	}
}

func TestValidateEndpointURLAllowsHTTPSAnywhere(t *testing.T) {
	s := FIELDS["runners.api_base_url"]
	if _, err := validate(s, "https://public-api.example.com/v1"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateEndpointURLRejectsPlaintextHTTPToPublicHost(t *testing.T) {
	s := FIELDS["runners.api_base_url"]
	if _, err := validate(s, "http://8.8.8.8:8080/v1"); err == nil {
		t.Error("expected rejection of plaintext http:// to a public IP")
	}
}

func TestValidateEndpointURLAllowsPlaintextHTTPToLoopback(t *testing.T) {
	s := FIELDS["runners.api_base_url"]
	if _, err := validate(s, "http://127.0.0.1:11434/v1"); err != nil {
		t.Errorf("unexpected error for loopback http://: %v", err)
	}
	if _, err := validate(s, "http://localhost:11434/v1"); err != nil {
		t.Errorf("unexpected error for localhost http://: %v", err)
	}
}

func TestValidateEndpointURLRejectsUserinfo(t *testing.T) {
	s := FIELDS["runners.api_base_url"]
	if _, err := validate(s, "https://user:pass@example.com/v1"); err == nil {
		t.Error("expected rejection of URL userinfo")
	}
}

func TestValidateTristateBool(t *testing.T) {
	s := FIELDS["gitlab.supports_work_item_hierarchy"]
	v, err := validate(s, true)
	if err != nil || *(v.(*bool)) != true {
		t.Errorf("v=%v err=%v", v, err)
	}
	v, err = validate(s, "false")
	if err != nil || *(v.(*bool)) != false {
		t.Errorf("v=%v err=%v", v, err)
	}
	if _, err := validate(s, "maybe"); err == nil {
		t.Error("expected rejection of an invalid tristate value")
	}
	v, err = validate(s, nil)
	if err != nil || v != (*bool)(nil) {
		t.Errorf("nil should validate to a nil *bool: v=%v err=%v", v, err)
	}
}

func TestValidateEnvVarNameList(t *testing.T) {
	s := FIELDS["runners.forward_env"]
	v, err := validate(s, "FOO,BAR, BAZ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	list := v.([]string)
	if len(list) != 3 || list[0] != "FOO" || list[2] != "BAZ" {
		t.Errorf("list = %v", list)
	}
	if _, err := validate(s, "FOO,not-a-valid-name!"); err == nil {
		t.Error("expected rejection of an invalid env var name")
	}
}

func TestValidateCommandAllowlistRejectsPaths(t *testing.T) {
	s := FIELDS["runners.api_command_allowlist"]
	if _, err := validate(s, "pytest,go/test"); err == nil {
		t.Error("expected rejection of a path-shaped entry")
	}
	v, err := validate(s, "pytest,go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if list := v.([]string); len(list) != 2 {
		t.Errorf("list = %v", list)
	}
}

// --- Secret-shaped key rejection ---

func TestRejectSecretShapedKeysTopLevel(t *testing.T) {
	err := rejectSecretShapedKeys(map[string]any{"token": "x"}, "", "test.yaml")
	if err == nil {
		t.Fatal("expected rejection of a top-level 'token' key")
	}
}

func TestRejectSecretShapedKeysNested(t *testing.T) {
	err := rejectSecretShapedKeys(map[string]any{"gitlab": map[string]any{"svc_token": "x"}}, "", "test.yaml")
	if err == nil {
		t.Fatal("expected rejection of a nested secret-shaped key")
	}
}

func TestRejectSecretShapedKeysInList(t *testing.T) {
	err := rejectSecretShapedKeys(map[string]any{
		"gitlab": map[string]any{"extra": []any{map[string]any{"api_key": "x"}}},
	}, "", "test.yaml")
	if err == nil {
		t.Fatal("expected rejection of a secret-shaped key nested inside a list")
	}
}

func TestRejectSecretShapedKeysAllowsSchemaVersion(t *testing.T) {
	err := rejectSecretShapedKeys(map[string]any{"schema_version": 1}, "", "test.yaml")
	if err != nil {
		t.Errorf("schema_version must not be rejected: %v", err)
	}
}

func TestRejectSecretShapedKeysAllowsOrdinaryKeys(t *testing.T) {
	err := rejectSecretShapedKeys(map[string]any{"gitlab": map[string]any{"base_url": "https://x"}}, "", "test.yaml")
	if err != nil {
		t.Errorf("unexpected rejection: %v", err)
	}
}

// --- Resolution precedence ---

func TestResolveSettingEnvVarWins(t *testing.T) {
	isolateConfigEnv(t)
	dir := makeGitCheckout(t)
	writeYAML(t, filepath.Join(dir, ".agents", "cadre.yaml"), "runners:\n  codex_profile: \"from-project\"\n")
	t.Setenv("SECURE_CLOUD_AGENTS_CODEX_PROFILE", "from-env")

	value, err := ResolveSetting("runners.codex_profile", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if value != "from-env" {
		t.Errorf("value = %v, want env var to win over project file", value)
	}
}

func TestResolveSettingProjectFileWhenNoEnv(t *testing.T) {
	isolateConfigEnv(t)
	dir := makeGitCheckout(t)
	writeYAML(t, filepath.Join(dir, ".agents", "cadre.yaml"), "gitlab:\n  supports_work_item_hierarchy: true\n")

	value, err := ResolveSetting("gitlab.supports_work_item_hierarchy", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, ok := value.(*bool)
	if !ok || b == nil || !*b {
		t.Errorf("value = %v, want true from project file", value)
	}
}

func TestResolveSettingGlobalFileWhenNoProjectOrEnv(t *testing.T) {
	isolateConfigEnv(t)
	dir := makeGitCheckout(t)
	globalDir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "cadre")
	writeYAML(t, filepath.Join(globalDir, "config.yaml"), "runners:\n  codex_profile: \"from-global\"\n")

	value, err := ResolveSetting("runners.codex_profile", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if value != "from-global" {
		t.Errorf("value = %v, want from-global", value)
	}
}

func TestResolveSettingStaticDefault(t *testing.T) {
	isolateConfigEnv(t)
	dir := makeGitCheckout(t)
	value, err := ResolveSetting("runners.claude_bin", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if value != "claude" {
		t.Errorf("value = %v, want the static default %q", value, "claude")
	}
}

func TestResolveSettingComputedDefault(t *testing.T) {
	isolateConfigEnv(t)
	dir := makeGitCheckout(t)
	value, err := ResolveSetting("agentic_sdlc.bin_path", dir)
	// No assertion on the value itself (depends on whether agentic-sdlc is
	// on this machine's PATH) -- just that resolution doesn't error when
	// nothing else is configured and the computed default is consulted.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = value
}

func TestResolveOptionalNilForRequiredFieldUnconfigured(t *testing.T) {
	isolateConfigEnv(t)
	dir := makeGitCheckout(t)
	t.Setenv("GITLAB_BASE_URL", "")
	os.Unsetenv("GITLAB_BASE_URL")
	value, err := ResolveOptional("gitlab.base_url", dir)
	if err != nil {
		t.Fatalf("ResolveOptional must never raise for an ordinary unconfigured outcome: %v", err)
	}
	if value != nil {
		t.Errorf("value = %v, want nil", value)
	}
}

func TestResolveSettingRequiredFieldFailsClosed(t *testing.T) {
	isolateConfigEnv(t)
	dir := makeGitCheckout(t)
	os.Unsetenv("GITLAB_BASE_URL")
	_, err := ResolveSetting("gitlab.base_url", dir)
	if err == nil {
		t.Fatal("expected an error for an unconfigured required field")
	}
}

// --- global_only scope enforcement (security-critical) ---

func TestGlobalOnlyFieldFromProjectFileIsScopeViolation(t *testing.T) {
	isolateConfigEnv(t)
	dir := makeGitCheckout(t)
	writeYAML(t, filepath.Join(dir, ".agents", "cadre.yaml"), "gitlab:\n  base_url: \"https://attacker.example.com\"\n")

	_, err := ResolveSetting("gitlab.base_url", dir)
	var scopeErr *SettingsScopeError
	if !errors.As(err, &scopeErr) {
		t.Fatalf("expected *SettingsScopeError, got %T: %v", err, err)
	}
}

func TestGlobalOnlyFieldExplicitNullFromProjectFileIsStillScopeViolation(t *testing.T) {
	// Deliberately fires on presence alone, not "found and non-null": an
	// explicit `null` still means this key was placed in an untrusted
	// project-local file for a field that may never be set there.
	isolateConfigEnv(t)
	dir := makeGitCheckout(t)
	writeYAML(t, filepath.Join(dir, ".agents", "cadre.yaml"), "gitlab:\n  base_url: null\n")

	_, err := ResolveSetting("gitlab.base_url", dir)
	var scopeErr *SettingsScopeError
	if !errors.As(err, &scopeErr) {
		t.Fatalf("expected *SettingsScopeError even for an explicit null, got %T: %v", err, err)
	}
}

func TestResolveOptionalNeverSwallowsScopeViolation(t *testing.T) {
	isolateConfigEnv(t)
	dir := makeGitCheckout(t)
	writeYAML(t, filepath.Join(dir, ".agents", "cadre.yaml"), "gitlab:\n  base_url: \"https://attacker.example.com\"\n")

	_, err := ResolveOptional("gitlab.base_url", dir)
	var scopeErr *SettingsScopeError
	if !errors.As(err, &scopeErr) {
		t.Fatalf("ResolveOptional must propagate a scope violation, not swallow it; got %T: %v", err, err)
	}
}

func TestProjectOrGlobalFieldFromProjectFileIsAllowed(t *testing.T) {
	isolateConfigEnv(t)
	dir := makeGitCheckout(t)
	writeYAML(t, filepath.Join(dir, ".agents", "cadre.yaml"), "gitlab:\n  supports_work_item_hierarchy: false\n")

	value, err := ResolveSetting("gitlab.supports_work_item_hierarchy", dir)
	if err != nil {
		t.Fatalf("project_or_global field must be settable from a project file: %v", err)
	}
	b := value.(*bool)
	if b == nil || *b != false {
		t.Errorf("value = %v", value)
	}
}

// --- write_setting ---

func TestWriteSettingGlobalTierAndReadBack(t *testing.T) {
	isolateConfigEnv(t)
	written, err := WriteSetting("runners.codex_profile", "my-profile", "global", "")
	if err != nil {
		t.Fatalf("WriteSetting: %v", err)
	}
	if !isFile(written) {
		t.Fatalf("expected a file at %s", written)
	}
	value, err := ResolveSetting("runners.codex_profile", "")
	if err != nil {
		t.Fatalf("ResolveSetting after write: %v", err)
	}
	if value != "my-profile" {
		t.Errorf("value = %v, want my-profile", value)
	}
}

func TestWriteSettingRejectsGlobalOnlyFieldAtProjectTier(t *testing.T) {
	isolateConfigEnv(t)
	dir := makeGitCheckout(t)
	_, err := WriteSetting("gitlab.base_url", "https://gitlab.example.com", "project", dir)
	if err == nil {
		t.Fatal("expected an error writing a global_only field to the project tier")
	}
}

func TestWriteSettingProjectTierPreservesUnknownKeys(t *testing.T) {
	isolateConfigEnv(t)
	dir := makeGitCheckout(t)
	path := filepath.Join(dir, ".agents", "cadre.yaml")
	writeYAML(t, path, "gitlab:\n  supports_work_item_hierarchy: true\nunknown_future_key: \"kept\"\n")
	ResetCache()

	if _, err := WriteSetting("runners.codex_profile", "", "project", dir); err == nil {
		t.Fatal("expected rejection: runners.codex_profile is global_only")
	}

	// Write a legitimately project-settable field and confirm the existing
	// unrelated key survives the rewrite.
	if _, err := WriteSetting("gitlab.supports_work_item_hierarchy", "false", "project", dir); err != nil {
		t.Fatalf("WriteSetting: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(data), "unknown_future_key") {
		t.Error("expected an unrelated existing key to survive the rewrite")
	}
}

func TestWriteSettingRejectsInvalidValue(t *testing.T) {
	isolateConfigEnv(t)
	_, err := WriteSetting("gitlab.base_url", "http://not-https.example.com", "global", "")
	if err == nil {
		t.Fatal("expected validation to reject a non-https gitlab.base_url on write")
	}
}

// --- ResolveMany / EffectiveSettings ---

func TestResolveManyAbortsOnFirstError(t *testing.T) {
	isolateConfigEnv(t)
	dir := makeGitCheckout(t)
	writeYAML(t, filepath.Join(dir, ".agents", "cadre.yaml"), "gitlab:\n  base_url: \"https://attacker.example.com\"\n")

	_, err := ResolveMany([]string{"runners.claude_bin", "gitlab.base_url"}, dir)
	if err == nil {
		t.Fatal("expected ResolveMany to propagate the scope violation on gitlab.base_url")
	}
}

func TestEffectiveSettingsNeverRaises(t *testing.T) {
	isolateConfigEnv(t)
	dir := makeGitCheckout(t)
	writeYAML(t, filepath.Join(dir, ".agents", "cadre.yaml"), "gitlab:\n  base_url: \"https://attacker.example.com\"\n")

	results := EffectiveSettings(dir)
	if len(results) != len(FIELDS) {
		t.Fatalf("expected %d results, got %d", len(FIELDS), len(results))
	}
	foundUnresolved := false
	for _, r := range results {
		if r.Key == "gitlab.base_url" {
			foundUnresolved = r.Origin == OriginUnresolved
		}
	}
	if !foundUnresolved {
		t.Error("expected gitlab.base_url to show as unresolved (scope violation), not crash the whole snapshot")
	}
}

// --- Config file discovery ---

func TestSelectExistingRejectsBothFilesPresent(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "config.yaml")
	jsonPath := filepath.Join(dir, "config.json")
	os.WriteFile(yamlPath, []byte("{}"), 0o644)
	os.WriteFile(jsonPath, []byte("{}"), 0o644)
	_, err := selectExisting(yamlPath, jsonPath, "test")
	if err == nil {
		t.Fatal("expected an error when both yaml and json exist at the same tier")
	}
}

func TestLoadConfigFileRejectsNonMapRoot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte("- 1\n- 2\n"), 0o644)
	_, err := loadConfigFile(path)
	if err == nil {
		t.Fatal("expected an error for a non-mapping root")
	}
}

func TestLoadConfigFileEmptyFileIsEmptyMap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(""), 0o644)
	data, err := loadConfigFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("expected an empty map, got %v", data)
	}
}

// --- Symlink escape guard ---

func TestRejectSymlinkEscapeOnReadCatchesEscape(t *testing.T) {
	if os.Getenv("CI") != "" {
		t.Skip("symlink creation may be restricted in some CI sandboxes")
	}
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret.yaml")
	os.WriteFile(outsideFile, []byte("gitlab:\n  base_url: \"https://evil.example.com\"\n"), 0o644)

	project := makeGitCheckout(t)
	agentsDir := filepath.Join(project, ".agents")
	os.MkdirAll(agentsDir, 0o755)
	symlinkPath := filepath.Join(agentsDir, "cadre.yaml")
	if err := os.Symlink(outsideFile, symlinkPath); err != nil {
		t.Skipf("cannot create symlink in this environment: %v", err)
	}

	_, err := rejectSymlinkEscapeOnRead(symlinkPath)
	if err == nil {
		t.Fatal("expected rejection of a symlink pointing outside the project root")
	}
}

// --- helpers ---

func writeYAML(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ResetCache()
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
