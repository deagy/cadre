// sync_codex_agents.go ports roster/orchestration/src/sync_codex_agents.py:
// safely installs the suite's namespaced Codex role wrappers into a Codex
// agents directory (normally ~/.codex/agents) without touching bare role
// files there that this suite does not own.
//
// Every destination write goes through WriteOwnedWrapper, the single write
// chokepoint mirroring the Python original's _write_owned_wrapper: a
// destination is only ever overwritten if it is either newly created or
// already carries this suite's PROVENANCE_MARKER -- so a hand-authored or
// differently-sourced Codex wrapper sharing a filename is never silently
// clobbered.
//
// Deviation from the Python original, deliberate and narrow: Python opens
// every source and destination file with O_NOFOLLOW where the platform
// offers it, closing the TOCTOU gap between "is this a symlink" and "open
// it" entirely. Go's syscall.O_NOFOLLOW is not defined on every platform
// this CLI cross-builds for (notably windows -- see the repository
// Makefile's cross-build target), so this port instead performs an
// os.Lstat symlink check immediately before opening, then verifies
// regular-file-ness again via os.Stat on the open handle. This narrows,
// rather than closes, the same race: a symlink swapped into place in the
// instant between the Lstat and the Open would still be followed. The
// post-open regular-file re-check still catches the common case (a
// symlink resolving to a non-regular target, e.g. a device or FIFO), and
// this is the same "document the narrower guarantee rather than silently
// weaken it" approach already applied elsewhere in this Go port (e.g.
// internal/initproject's suiteCheckoutRoot).
package orchestration

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ProvenanceMarker is the comment line every generated wrapper carries.
// WriteOwnedWrapper treats its presence in an existing destination file as
// proof this suite owns that file and may overwrite it.
const ProvenanceMarker = "# GENERATED FILE: canonical source is roster/"

const (
	indexFilename      = "agents-index.json"
	indexSchemaVersion = 1
	wrapperNamePrefix  = "agents-"
	wrapperNameSuffix  = ".toml"
	wrapperGlobPattern = "agents-*.toml"
)

var modelLinePattern = regexp.MustCompile(`(?m)^model\s*=\s*(".*")\s*$`)

// readRegularFile reads path, refusing a symlink or a non-regular target.
// See this file's package doc for the narrowed-vs-Python TOCTOU note.
func readRegularFile(path string) ([]byte, error) {
	if info, err := os.Lstat(path); err != nil {
		return nil, err
	} else if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("refusing non-regular source wrapper: %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("refusing non-regular source wrapper: %s", path)
	}
	return readAll(f)
}

func readAll(f *os.File) ([]byte, error) {
	var out []byte
	buf := make([]byte, 1024*1024)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return out, nil
			}
			return nil, err
		}
	}
}

// writeOwnedWrapper writes content to destination, refusing to overwrite a
// symlink or a destination file this suite does not own (proven by the
// presence of ProvenanceMarker). Returns "installed" or "unchanged".
func writeOwnedWrapper(destination string, content []byte) (string, error) {
	if info, err := os.Lstat(destination); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("refusing symlinked destination wrapper: %s", destination)
	}

	f, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err == nil {
		defer func() { _ = f.Close() }()
		info, err := f.Stat()
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("refusing non-regular destination wrapper: %s", destination)
		}
		if _, err := f.Write(content); err != nil {
			return "", err
		}
		return "installed", nil
	}
	if !os.IsExist(err) {
		return "", err
	}

	f, err = os.OpenFile(destination, os.O_RDWR, 0o644)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("refusing non-regular destination wrapper: %s", destination)
	}
	existing, err := readAll(f)
	if err != nil {
		return "", err
	}
	if !strings.Contains(string(existing), ProvenanceMarker) {
		return "", fmt.Errorf("refusing to overwrite unowned namespaced Codex wrapper: %s", destination)
	}
	if string(existing) == string(content) {
		return "unchanged", nil
	}
	if err := f.Truncate(0); err != nil {
		return "", err
	}
	if _, err := f.Seek(0, 0); err != nil {
		return "", err
	}
	if _, err := f.Write(content); err != nil {
		return "", err
	}
	return "installed", nil
}

func roleIDFromWrapperName(name string) string {
	return strings.TrimSuffix(strings.TrimPrefix(name, wrapperNamePrefix), wrapperNameSuffix)
}

// extractModel is a best-effort extraction of the `model = "..."` line from
// a generated wrapper. The wrapper format is a small, fixed,
// generator-produced subset of TOML, so a targeted line scan is sufficient.
func extractModel(content []byte) (string, bool) {
	match := modelLinePattern.FindSubmatch(content)
	if match == nil {
		return "", false
	}
	var model string
	if err := json.Unmarshal(match[1], &model); err != nil {
		return "", false
	}
	return model, true
}

// RoleIndexEntry is one role's entry in agents-index.json.
type RoleIndexEntry struct {
	Path  string  `json:"path"`
	Model *string `json:"model"`
}

// AgentsIndex is the generated agents-index.json content.
type AgentsIndex struct {
	GeneratedMarker string                    `json:"generated_marker"`
	SchemaVersion   int                       `json:"schema_version"`
	Roles           map[string]RoleIndexEntry `json:"roles"`
}

type wrapperContent struct {
	path    string
	name    string
	content []byte
}

func buildIndex(contents []wrapperContent, target string) (*AgentsIndex, error) {
	roles := map[string]RoleIndexEntry{}
	for _, w := range contents {
		roleID := roleIDFromWrapperName(w.name)
		destination, err := filepath.Abs(filepath.Join(target, w.name))
		if err != nil {
			return nil, err
		}
		var model *string
		if m, ok := extractModel(w.content); ok {
			model = &m
		}
		roles[roleID] = RoleIndexEntry{Path: destination, Model: model}
	}
	return &AgentsIndex{GeneratedMarker: ProvenanceMarker, SchemaVersion: indexSchemaVersion, Roles: roles}, nil
}

// SyncResult reports what SyncWrappers did.
type SyncResult struct {
	Installed   []string `json:"installed"`
	Unchanged   []string `json:"unchanged"`
	IndexStatus string   `json:"index_status"`
	IndexPath   string   `json:"index_path"`
}

// SyncWrappers installs every roster/provider/codex-agents/agents-*.toml
// wrapper from source into target, then writes target/agents-index.json.
// The index is only built and written after every wrapper write below has
// succeeded, so an interrupted run never leaves the index describing
// wrappers that were not actually installed.
func SyncWrappers(source, target string) (*SyncResult, error) {
	matches, err := filepath.Glob(filepath.Join(source, wrapperGlobPattern))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		return nil, fmt.Errorf("no namespaced Codex wrappers found under %s", source)
	}

	var contents []wrapperContent
	for _, wrapper := range matches {
		content, err := readRegularFile(wrapper)
		if err != nil {
			return nil, err
		}
		contents = append(contents, wrapperContent{path: wrapper, name: filepath.Base(wrapper), content: content})
	}

	if err := os.MkdirAll(target, 0o755); err != nil {
		return nil, err
	}
	var installed, unchanged []string
	for _, w := range contents {
		destination := filepath.Join(target, w.name)
		status, err := writeOwnedWrapper(destination, w.content)
		if err != nil {
			return nil, err
		}
		if status == "unchanged" {
			unchanged = append(unchanged, destination)
		} else {
			installed = append(installed, destination)
		}
	}

	index, err := buildIndex(contents, target)
	if err != nil {
		return nil, err
	}
	indexJSON, err := marshalIndexSorted(index)
	if err != nil {
		return nil, err
	}
	indexDestination := filepath.Join(target, indexFilename)
	indexStatus, err := writeOwnedWrapper(indexDestination, indexJSON)
	if err != nil {
		return nil, err
	}

	if installed == nil {
		installed = []string{}
	}
	if unchanged == nil {
		unchanged = []string{}
	}
	return &SyncResult{Installed: installed, Unchanged: unchanged, IndexStatus: indexStatus, IndexPath: indexDestination}, nil
}

// marshalIndexSorted renders index as indent=2, sort_keys=True JSON with a
// trailing newline, matching Python's json.dumps(index, indent=2,
// sort_keys=True) + "\n" exactly (Go's encoding/json already sorts map
// keys during Marshal).
func marshalIndexSorted(index *AgentsIndex) ([]byte, error) {
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
