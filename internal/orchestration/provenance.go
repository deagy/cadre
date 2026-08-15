// provenance.go ports roster/orchestration/src/provenance.py: suite-input
// provenance binding for dispatch plans.
//
// A sha256 content hash of the exact catalog.yaml/routing-configuration
// bytes a dispatch plan was built from, plus a best-effort git commit
// identity, assembled into the plan's Provenance field.
//
// This is a distinct, sibling concern to a plan's own self-consistency
// checksum (not implemented here): provenance binds the plan to the
// suite-input *content* that produced it -- which exact catalog.yaml/
// routing configuration, and which git commit, generated this artifact --
// independently verifiable by an auditor who recomputes sha256sum/
// git rev-parse HEAD against a historical checkout, without needing to
// trust the generating process.
//
// Never fabricate a value: fields are omitted entirely (via `omitempty`)
// when there is no actual causal read path behind them, rather than filled
// with a placeholder.
package orchestration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// GitTimeout bounds the best-effort git invocations used for provenance.
const GitTimeout = 10 * time.Second

// Provenance is the dispatch plan's `provenance` object.
type Provenance struct {
	CatalogContentHash string `json:"catalog_content_hash"`
	RoutingContentHash string `json:"routing_content_hash"`
	OverlayApplied     bool   `json:"overlay_applied,omitempty"`
	OverlayPath        string `json:"overlay_path,omitempty"`
	OverlayContentHash string `json:"overlay_content_hash,omitempty"`
	GitCommitSHA       string `json:"git_commit_sha,omitempty"`
	// A pointer, not a plain slice: provenance.py emits git_dirty_paths
	// whenever `git status` *succeeded*, including as an empty list for a
	// clean tree, and omits it only when the command could not run. With
	// `[]string` plus omitempty those two cases are indistinguishable, so a
	// clean checkout silently dropped the key -- a difference invisible to
	// the plan's fingerprint, which excludes provenance entirely.
	GitDirtyPaths              *[]string `json:"git_dirty_paths,omitempty"`
	AgenticSDLCContractVersion *int      `json:"agentic_sdlc_contract_version,omitempty"`
}

// HashFile returns "sha256:<hex>" over the exact bytes at path.
//
// Deliberately propagates on a read failure (missing/unreadable file):
// catalog.yaml and the routing configuration are already mandatory reads
// for plan generation to proceed at all, so hashing adds no new failure
// surface here -- it must not silently degrade to a placeholder value.
func HashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// runGit is a best-effort git invocation that never returns an error to the
// caller; it degrades to ("", false) on any failure. Mirrors
// select_agents's git hardening against a caller-controlled checkout
// (neutralized system config, no interactive credential prompts, no
// fsmonitor hook, no optional locks) but always degrades instead of
// propagating, because git identity is supplementary provenance and must
// never turn a missing/unavailable git checkout into a hard plan-generation
// failure.
func runGit(args []string, cwd string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), GitTimeout)
	defer cancel()

	fullArgs := append([]string{"-c", "core.fsmonitor=false", "--no-optional-locks"}, args...)
	cmd := exec.CommandContext(ctx, "git", fullArgs...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		return "", false
	}
	return stdout.String(), true
}

// GitIdentity returns a best-effort git commit SHA and the (sorted,
// deduplicated) set of dirty paths among exactly catalogPath and
// routingPath -- not the whole working tree, so an unrelated dirty file
// elsewhere in the checkout doesn't make an unrelated plan look suspect.
//
// ok is false (and both other return values empty) when catalogPath's
// parent directory is not inside a resolvable git working tree, the git
// binary is unavailable, or the lookup fails for any other reason. Invoked
// with catalogPath's parent as the git working directory -- git itself
// walks upward to find the enclosing .git boundary.
func GitIdentity(catalogPath, routingPath string) (commitSHA string, dirtyPaths []string, ok bool) {
	cwd := filepath.Dir(catalogPath)

	out, success := runGit([]string{"rev-parse", "HEAD"}, cwd)
	if !success {
		return "", nil, false
	}
	sha := strings.TrimSpace(out)
	if sha == "" {
		return "", nil, false
	}

	status, statusOK := runGit(
		[]string{"-c", "status.relativePaths=true", "status", "--short", "--", catalogPath, routingPath},
		cwd,
	)
	var dirty []string
	if statusOK {
		// Non-nil even when nothing is dirty: a clean tree must report an
		// empty list, not an absent key. `nil` is reserved for "git status
		// could not run", which is a different fact about the checkout.
		dirty = []string{}
		seen := make(map[string]bool)
		for _, line := range strings.Split(status, "\n") {
			if strings.TrimSpace(line) == "" || len(line) <= 3 {
				continue
			}
			path := strings.TrimSpace(line[3:])
			if path != "" && !seen[path] {
				seen[path] = true
				dirty = append(dirty, path)
			}
		}
		sort.Strings(dirty)
	}

	return sha, dirty, true
}

// BuildProvenanceOptions collects BuildProvenance's optional inputs.
type BuildProvenanceOptions struct {
	OverlayPath                string // "" if no overlay was applied.
	AgenticSDLCContractVersion *int   // nil if no lifecycle contract was consulted.
}

// BuildProvenance assembles a dispatch plan's provenance object.
//
// Always includes catalog_content_hash/routing_content_hash (mandatory
// whenever provenance is computed at all -- see HashFile's fail-hard
// contract). Adds best-effort git identity when available, and the
// already-consumed lifecycle contract version when the caller supplies one.
func BuildProvenance(catalogPath, routingPath string, opts BuildProvenanceOptions) (*Provenance, error) {
	catalogHash, err := HashFile(catalogPath)
	if err != nil {
		return nil, err
	}
	routingHash, err := HashFile(routingPath)
	if err != nil {
		return nil, err
	}

	prov := &Provenance{
		CatalogContentHash: catalogHash,
		RoutingContentHash: routingHash,
	}

	if opts.OverlayPath != "" {
		// routing_content_hash stays the *base* file's hash: an auditor
		// needs both halves to reproduce the merge, not a hash of a merged
		// artifact that exists on no disk.
		overlayHash, err := HashFile(opts.OverlayPath)
		if err != nil {
			return nil, err
		}
		prov.OverlayApplied = true
		prov.OverlayPath = opts.OverlayPath
		prov.OverlayContentHash = overlayHash
	}

	if sha, dirty, ok := GitIdentity(catalogPath, routingPath); ok {
		prov.GitCommitSHA = sha
		if dirty != nil {
			prov.GitDirtyPaths = &dirty
		}
	}

	if opts.AgenticSDLCContractVersion != nil {
		prov.AgenticSDLCContractVersion = opts.AgenticSDLCContractVersion
	}

	return prov, nil
}
