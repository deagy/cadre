// files.go ports settings.py's config-file discovery, loading, and the
// secret-shaped-key rejection that applies to every file this package
// reads.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/deagy/cadre/cli/internal/platform"
	"gopkg.in/yaml.v3"
)

const (
	projectConfigBasename = "cadre"
	projectConfigDir      = ".agents"
	globalConfigAppDir    = "cadre"
	globalConfigBasename  = "config"
)

// ---------------------------------------------------------------------
// Secret-shaped key rejection -- never read from, or written to, a config
// file this package manages.
// ---------------------------------------------------------------------

var secretLeafPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^token$`),
	regexp.MustCompile(`.*_token$`),
	regexp.MustCompile(`^api_key$`),
	regexp.MustCompile(`^password$`),
	regexp.MustCompile(`^secret$`),
	regexp.MustCompile(`^svc_token$`),
}

func looksLikeSecretKey(leaf string) bool {
	for _, p := range secretLeafPatterns {
		if p.MatchString(leaf) {
			return true
		}
	}
	return false
}

// rejectSecretShapedKeys walks both maps and sequences (a secret-shaped key
// nested under a list must be caught too, not only top-level/dict-nested
// ones -- otherwise a config-file rewrite that preserves unknown keys would
// silently round-trip a pasted credential forever).
func rejectSecretShapedKeys(data map[string]any, pathPrefix string, filePath string) error {
	for key, value := range data {
		if key == "schema_version" {
			continue
		}
		dotted := key
		if pathPrefix != "" {
			dotted = pathPrefix + "." + key
		}
		if looksLikeSecretKey(key) {
			return settingsErrorf(
				"%s: key %q looks like a secret (matches a *_token/*.token/*.api_key/*.password/"+
					"*.secret pattern) and must never be stored in a cadre config file; secrets are "+
					"always read from an environment variable", filePath, dotted)
		}
		if err := rejectSecretShapedKeysInValue(value, dotted, filePath); err != nil {
			return err
		}
	}
	return nil
}

func rejectSecretShapedKeysInValue(value any, dotted, filePath string) error {
	switch v := value.(type) {
	case map[string]any:
		return rejectSecretShapedKeys(v, dotted, filePath)
	case []any:
		for i, item := range v {
			if err := rejectSecretShapedKeysInValue(item, dotted+"["+itoa(i)+"]", filePath); err != nil {
				return err
			}
		}
	}
	return nil
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}

// ---------------------------------------------------------------------
// Filesystem-identity containment (settings.py's _is_same_or_descendant /
// _resolve_existing_ancestor), shared with write.go and, longer-term, with
// routing_overlay.go's/init_project.go's own containment needs.
// ---------------------------------------------------------------------

// resolveExistingAncestor returns the nearest existing ancestor of path (or
// path itself, if it already exists). Used so filesystem-identity
// comparisons still work against a path that does not exist yet (e.g. a
// write target about to be created), by anchoring the comparison at
// whatever prefix of it is already real on disk.
// ResolveExistingAncestor is the exported form of resolveExistingAncestor,
// for callers (internal/initproject) that need this package's filesystem-
// identity containment primitives directly -- mirrors init_project.py
// reaching into resolve.py's underscore-prefixed
// _resolve_existing_ancestor.
func ResolveExistingAncestor(path string) string { return resolveExistingAncestor(path) }

// IsSameOrDescendant is the exported form of isSameOrDescendant; see
// ResolveExistingAncestor's doc comment.
func IsSameOrDescendant(path, ancestor string) bool { return isSameOrDescendant(path, ancestor) }

func resolveExistingAncestor(path string) string {
	current, err := filepath.Abs(path)
	if err != nil {
		current = path
	}
	for {
		if _, err := os.Lstat(current); err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err == nil {
				return resolved
			}
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return current
		}
		current = parent
	}
}

// isSameOrDescendant is a filesystem-identity containment check: true if
// path IS ancestor, or is located under it. Uses device/inode identity
// (os.SameFile) rather than string/resolved-path equality, so it isn't
// fooled by a case-insensitive filesystem where two differently-cased
// paths are actually the same on-disk directory. ancestor is required to
// already exist; path may not exist yet (its nearest existing ancestor is
// used as the anchor for the walk up).
func isSameOrDescendant(path, ancestor string) bool {
	ancestorAbs, err := filepath.Abs(ancestor)
	if err != nil {
		return false
	}
	ancestorResolved, err := filepath.EvalSymlinks(ancestorAbs)
	if err != nil {
		ancestorResolved = ancestorAbs
	}
	ancestorInfo, err := os.Stat(ancestorResolved)
	if err != nil {
		return false
	}

	probe := resolveExistingAncestor(path)
	for {
		probeInfo, err := os.Stat(probe)
		if err == nil && os.SameFile(probeInfo, ancestorInfo) {
			return true
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return false
		}
		probe = parent
	}
}

// findFileAtProjectRootConfig delegates to platform.FindFileAtProjectRoot,
// the single shared implementation of the walk-up-to-.git discovery
// convention used across this repository's project-local override
// mechanisms (see that function's doc comment).
func findFileAtProjectRootConfig(relativePath, start string) (string, bool) {
	return platform.FindFileAtProjectRoot(relativePath, start)
}

func findProjectGitRoot(start string) (string, bool) {
	root, err := platform.FindProjectRoot(start)
	if err != nil {
		return "", false
	}
	return root, true
}

func defaultComputeRosterRoot() (any, bool) {
	root, ok := findProjectGitRoot("")
	if !ok {
		return nil, false
	}
	return filepath.Join(root, "roster"), true
}

// ---------------------------------------------------------------------
// Symlink-escape guard on read.
// ---------------------------------------------------------------------

// rejectSymlinkEscapeOnRead guards the read path the same way write_setting
// guards the write path: discovery's file-exists check follows symlinks,
// so a malicious .agents/cadre.yaml (or a symlinked .agents directory)
// shipped in an untrusted, clonable project can point outside the project
// entirely. Reject that before the file is ever opened/parsed.
func rejectSymlinkEscapeOnRead(candidate string) (string, error) {
	// relativePath passed to findFileAtProjectRootConfig is always exactly
	// projectConfigDir/<basename>.<ext> (two path components), so the
	// directory this candidate was actually discovered under -- before any
	// symlink is followed -- is two levels up from the candidate.
	root := filepath.Dir(filepath.Dir(candidate))
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	resolvedCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		resolvedCandidate = candidate
	}
	if !isSameOrDescendant(resolvedCandidate, rootAbs) {
		return "", settingsErrorf(
			"%s resolves outside of %s (via a symlink); a project-local cadre config "+
				"file/directory may not point outside the project it was found in", candidate, rootAbs)
	}
	return candidate, nil
}

// ---------------------------------------------------------------------
// Discovery + loading, cached per process (mirrors settings.py's
// module-level _FILE_CACHE / reset_cache()).
// ---------------------------------------------------------------------

var (
	fileCacheMu sync.Mutex
	fileCache   = map[string]map[string]any{}
)

// ResetCache clears the per-process config-file cache. Call after
// WriteSetting, and at the start of any test that needs isolation from a
// prior test's resolved config-file state.
func ResetCache() {
	fileCacheMu.Lock()
	defer fileCacheMu.Unlock()
	fileCache = map[string]map[string]any{}
}

func globalConfigDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		expanded, err := expandHome(xdg)
		if err == nil {
			return filepath.Join(expanded, globalConfigAppDir)
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", globalConfigAppDir)
}

func expandHome(p string) (string, error) {
	if !strings.HasPrefix(p, "~") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p, err
	}
	if p == "~" {
		return home, nil
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:]), nil
	}
	return p, nil
}

func projectConfigCandidates(start string) (yamlPath, jsonPath string, err error) {
	if start == "" && projectTierCWDFallbackDisabled() {
		return "", "", nil
	}
	yamlCandidate, yamlOK := findFileAtProjectRootConfig(filepath.Join(projectConfigDir, projectConfigBasename+".yaml"), start)
	jsonCandidate, jsonOK := findFileAtProjectRootConfig(filepath.Join(projectConfigDir, projectConfigBasename+".json"), start)
	if yamlOK {
		yamlCandidate, err = rejectSymlinkEscapeOnRead(yamlCandidate)
		if err != nil {
			return "", "", err
		}
	} else {
		yamlCandidate = ""
	}
	if jsonOK {
		jsonCandidate, err = rejectSymlinkEscapeOnRead(jsonCandidate)
		if err != nil {
			return "", "", err
		}
	} else {
		jsonCandidate = ""
	}
	return yamlCandidate, jsonCandidate, nil
}

func globalConfigCandidates() (yamlPath, jsonPath string) {
	dir := globalConfigDir()
	return filepath.Join(dir, globalConfigBasename+".yaml"), filepath.Join(dir, globalConfigBasename+".json")
}

func selectExisting(yamlPath, jsonPath, tierLabel string) (string, error) {
	yamlExists := yamlPath != "" && isFile(yamlPath)
	jsonExists := jsonPath != "" && isFile(jsonPath)
	if yamlExists && jsonExists {
		return "", settingsErrorf("both %s and %s exist; only one %s cadre config file may exist at a time -- remove one", yamlPath, jsonPath, tierLabel)
	}
	if yamlExists {
		return yamlPath, nil
	}
	if jsonExists {
		return jsonPath, nil
	}
	return "", nil
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func loadConfigFile(path string) (map[string]any, error) {
	fileCacheMu.Lock()
	if cached, ok := fileCache[path]; ok {
		fileCacheMu.Unlock()
		return cached, nil
	}
	fileCacheMu.Unlock()

	text, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	switch {
	case strings.TrimSpace(string(text)) == "":
		data = map[string]any{}
	case strings.EqualFold(filepath.Ext(path), ".json"):
		var parsed any
		if err := json.Unmarshal(text, &parsed); err != nil {
			return nil, settingsErrorf("%s: not valid JSON", path)
		}
		m, ok := parsed.(map[string]any)
		if !ok {
			return nil, settingsErrorf("%s: root of a cadre config file must be a mapping", path)
		}
		data = m
	default:
		var parsed any
		if err := yaml.Unmarshal(text, &parsed); err != nil {
			return nil, settingsErrorf("%s: not valid YAML", path)
		}
		if parsed == nil {
			data = map[string]any{}
		} else {
			m, ok := parsed.(map[string]any)
			if !ok {
				return nil, settingsErrorf("%s: root of a cadre config file must be a mapping", path)
			}
			data = m
		}
	}

	if err := rejectSecretShapedKeys(data, "", path); err != nil {
		return nil, err
	}

	fileCacheMu.Lock()
	fileCache[path] = data
	fileCacheMu.Unlock()
	return data, nil
}

// ProjectConfigPath returns the resolved project-local config file path, or
// "" if absent.
func ProjectConfigPath(start string) (string, error) {
	yamlPath, jsonPath, err := projectConfigCandidates(start)
	if err != nil {
		return "", err
	}
	return selectExisting(yamlPath, jsonPath, "project-local")
}

// GlobalConfigPath returns the resolved (existing-or-not) user-global
// config file path. Returns the .yaml candidate when neither file exists,
// since that is where a fresh write goes by default.
func GlobalConfigPath() (string, error) {
	yamlPath, jsonPath := globalConfigCandidates()
	existing, err := selectExisting(yamlPath, jsonPath, "user-global")
	if err != nil {
		return "", err
	}
	if existing != "" {
		return existing, nil
	}
	return yamlPath, nil
}

func lookupNested(data map[string]any, key string) (found bool, value any) {
	parts := strings.Split(key, ".")
	var node any = data
	for _, part := range parts {
		m, ok := node.(map[string]any)
		if !ok {
			return false, nil
		}
		v, present := m[part]
		if !present {
			return false, nil
		}
		node = v
	}
	return true, node
}
