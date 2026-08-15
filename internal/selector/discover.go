package selector

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// This file ports the selector's git-derived inputs: the changed-file set when
// --files is not supplied, and the knowledge-store sources when --source is
// not supplied.
//
// Both feed the hashed payload, so both are parity surfaces, and both read a
// checkout the caller named with --root. That checkout is not necessarily
// trusted -- see runGit.

// runGit invokes git against repositoryRoot with the same hardening the Python
// implementation applies.
//
// --root is caller-controlled and may point at an untrusted checkout, so this
// neutralises the config-driven execution surface before reading any .git
// state: GIT_CONFIG_NOSYSTEM=1 drops system-wide config, core.fsmonitor=false
// prevents a repository-supplied fsmonitor hook from running, and
// GIT_TERMINAL_PROMPT=0 keeps a credential prompt from blocking a
// non-interactive selection run. --no-optional-locks leaves the target
// checkout's index alone; selection reads, it never mutates.
func runGit(repositoryRoot string, args ...string) (string, error) {
	full := append([]string{"-c", "core.fsmonitor=false", "--no-optional-locks"}, args...)
	command := exec.Command("git", full...)
	command.Dir = repositoryRoot
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")

	var stderr strings.Builder
	command.Stderr = &stderr
	stdout, err := command.Output()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = fmt.Sprintf("git %s failed", strings.Join(args, " "))
		}
		return "", fmt.Errorf("%s", message)
	}
	return string(stdout), nil
}

// ChangedFiles is the changed-file set plus the label recording how it was
// obtained. The label reaches the plan, so it is part of the contract.
type ChangedFiles struct {
	Source string
	Files  []string
}

// DiscoverChangedFiles reproduces discover_changed_files.
//
// With a base ref the question is "what does this branch change", answered by
// a three-dot diff. Without one it is "what is uncommitted here", answered by
// status including untracked files -- a new file is exactly the kind of change
// routing most needs to see.
func DiscoverChangedFiles(base, repositoryRoot string) (ChangedFiles, error) {
	if base != "" {
		spec := base + "...HEAD"
		out, err := runGit(repositoryRoot, "diff", "--name-only", spec)
		if err != nil {
			return ChangedFiles{}, err
		}
		var files []string
		for _, line := range strings.Split(out, "\n") {
			if line != "" {
				files = append(files, line)
			}
		}
		return ChangedFiles{Source: "git-diff:" + spec, Files: files}, nil
	}

	// -z gives NUL-separated, never-quoted paths. git's default --short
	// applies core.quotePath, which wraps any path containing non-ASCII or
	// special characters in quotes with C-style escapes; parsing that as a
	// plain line would put a mangled path into the plan rather than failing.
	out, err := runGit(repositoryRoot, "status", "--short", "-z", "--untracked-files=all")
	if err != nil {
		return ChangedFiles{}, err
	}
	fields := strings.Split(out, "\x00")
	var files []string
	for index := 0; index < len(fields); index++ {
		entry := fields[index]
		if entry == "" {
			continue
		}
		// "XY path", so the status is the first two bytes and the path starts
		// at the fourth. A rename or copy appends the *original* path as one
		// extra NUL-separated field, which is not itself a changed file and
		// must be stepped over rather than routed on.
		if len(entry) < 3 {
			continue
		}
		status, path := entry[:2], entry[3:]
		files = append(files, path)
		if strings.ContainsAny(status, "RC") {
			index++
		}
	}
	return ChangedFiles{Source: "git-status", Files: files}, nil
}

// ExplicitFiles flattens repeatable --files values, each of which may itself
// be comma-separated, and de-duplicates while preserving first-seen order.
//
// The de-duplication is not cosmetic: changed_files reaches the hashed
// payload, so naming one path twice would otherwise produce a different
// dispatch_fingerprint for an identical set of changes.
func ExplicitFiles(values []string) []string {
	var files []string
	seen := map[string]bool{}
	for _, value := range values {
		for _, entry := range strings.Split(value, ",") {
			entry = strings.TrimSpace(entry)
			if entry == "" || seen[entry] {
				continue
			}
			seen[entry] = true
			files = append(files, entry)
		}
	}
	return files
}

// StagedKnowledgeSource mirrors accepted_ingest.STAGED_SOURCE, the dedicated
// source steward-accepted findings are ingested under.
//
// Duplicated rather than derived, exactly as the Python side duplicates it,
// and drifting from it silently scopes retrieval away from the
// steward-accepted half of the store.
const StagedKnowledgeSource = "proposed-knowledge"

// projectLocalKnowledgeConfig mirrors config.PROJECT_LOCAL_RELATIVE_PATH. Its
// presence is what makes the store resolve to a project-local partition, and
// therefore what makes StagedKnowledgeSource a legal source to name.
var projectLocalKnowledgeConfig = filepath.Join(".agents", "knowledge-store", "config.json")

var (
	gitSuffixPattern = regexp.MustCompile(`(?i)\.git$`)
	originSlugShape  = regexp.MustCompile(`^[a-z0-9._-]+/[a-z0-9._-]+$`)
	nonSlugRun       = regexp.MustCompile(`[^a-z0-9._-]+`)
)

// OriginSlug derives an "owner/repository" identity from the checkout's origin
// remote, or reports that it could not.
//
// Accepts https://host/owner/repo.git, ssh://git@host/owner/repo.git and
// SCP-style git@host:owner/repo.git. A remote that does not reduce to exactly
// that shape yields no slug rather than a guess, because this string scopes
// knowledge retrieval -- a wrong one reads another project's corpus.
func OriginSlug(repositoryRoot string) (string, bool) {
	out, err := runGit(repositoryRoot, "remote", "get-url", "origin")
	if err != nil {
		return "", false
	}
	origin := strings.TrimSpace(out)
	if origin == "" {
		return "", false
	}

	var path string
	if strings.Contains(origin, "://") {
		parsed, err := url.Parse(origin)
		if err != nil {
			return "", false
		}
		path = parsed.Path
	} else {
		// SCP-style: everything after the first colon. Split on the *first*
		// colon so a path containing one does not truncate the repository.
		if index := strings.Index(origin, ":"); index >= 0 {
			path = origin[index+1:]
		} else {
			path = origin
		}
	}

	var parts []string
	for _, part := range strings.Split(strings.Trim(path, "/"), "/") {
		if part != "" {
			parts = append(parts, part)
		}
	}
	if len(parts) < 2 {
		return "", false
	}
	owner := parts[len(parts)-2]
	repository := gitSuffixPattern.ReplaceAllString(parts[len(parts)-1], "")
	if owner == "" || repository == "" {
		return "", false
	}
	slug := strings.ToLower(owner + "/" + repository)
	if !originSlugShape.MatchString(slug) {
		return "", false
	}
	return slug, true
}

// ResolveProjectKnowledgeSource is the project's own ingested-corpus source:
// its origin slug, or a stable local identifier for a checkout with no usable
// origin. The digest keeps two same-named directories from colliding onto one
// source name.
func ResolveProjectKnowledgeSource(repositoryRoot string) string {
	if slug, ok := OriginSlug(repositoryRoot); ok {
		return slug
	}
	resolved := repositoryRoot
	if absolute, err := filepath.Abs(repositoryRoot); err == nil {
		resolved = absolute
	}
	sum := sha256.Sum256([]byte(resolved))
	digest := hex.EncodeToString(sum[:])[:12]

	basename := strings.Trim(nonSlugRun.ReplaceAllString(
		strings.ToLower(filepath.Base(repositoryRoot)), "-"), "-")
	if basename == "" {
		basename = "repository"
	}
	return fmt.Sprintf("local-%s-%s", basename, digest)
}

// HasProjectLocalKnowledgeStore reports whether this repository claims its own
// knowledge-store partition.
func HasProjectLocalKnowledgeStore(repositoryRoot string) bool {
	info, err := os.Stat(filepath.Join(repositoryRoot, projectLocalKnowledgeConfig))
	return err == nil && !info.IsDir()
}

// ResolveKnowledgeSources is every source a dispatched agent should retrieve
// from, primary first.
//
// Two, not one, wherever the second is legal: steward-accepted findings land
// under a deliberately separate source so they are reached by name rather than
// by accident, and naming it here is reaching it by name.
//
// It is omitted for a project with no store of its own. Staged records are
// per project, and the store refuses to read that source name from the shared
// global-fallback store -- refusal being per call rather than per source, a
// plan that named it unconditionally would return the agent nothing at all,
// including the project's own corpus it would otherwise have retrieved.
func ResolveKnowledgeSources(repositoryRoot string) []string {
	sources := []string{ResolveProjectKnowledgeSource(repositoryRoot)}
	if HasProjectLocalKnowledgeStore(repositoryRoot) {
		sources = append(sources, StagedKnowledgeSource)
	}
	return sources
}

// NormalizeExplicitSources de-duplicates --source values in first-seen order,
// matching the store's own normalize_sources, and rejects an empty one.
//
// An empty value is refused rather than skipped: `--source ""` would otherwise
// produce source_filter: [""], a plan that violates its own schema and whose
// argv the store rejects only at execution -- after the invalid plan has been
// emitted and possibly consumed.
func NormalizeExplicitSources(values []string) ([]string, error) {
	for index, value := range values {
		if strings.TrimSpace(value) == "" {
			//nolint:staticcheck // ST1005: ported verbatim from select_agents.py;
			// rewording user-facing text mid-port is the drift this exists to avoid.
			return nil, fmt.Errorf(
				"--source must be a non-empty knowledge-store source name; argument "+
					"%d was empty. Omit --source entirely to use this repository's "+
					"default sources.", index+1)
		}
	}
	var sources []string
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		sources = append(sources, value)
	}
	return sources, nil
}
