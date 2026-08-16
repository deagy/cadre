package kernel

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Shared test fixtures, mock builders and utilities.
//
// These were spread across the `*_differential_test.go` files, each landing
// wherever the first test that needed it happened to live. That was fine while
// those files were permanent; it stops being fine the moment they are deleted
// with the Python kernel they compare against, because most of what a
// *surviving* test needs would go with them.
//
// So they live here instead, and the deletion becomes a file removal rather
// than a refactor tangled up with one. Nothing here talks to Python: the
// planned-project fixture is built by this kernel in-process, which is also
// what stopped a missing Python turning the differentials into silent passes.

// The identities the shared fixtures are built around. Named here rather than
// beside the first test that happened to need them: every fixture below refers
// to them, and so do half the surviving tests.
const (
	decideTask     = "TASK-1"
	decideActor    = "github.com/product-owner"
	decideEvidence = "github-review:acme/app:pull/1:review/1:reviewer/product-owner"
	decideWhen     = "2026-08-15T09:00:00+00:00"
)

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// initialisedProject builds a bare project overlay -- no planned task -- and
// returns a fresh copy of it.

const frozenMoment = "2026-08-15T09:00:00.000000Z"

// freezeClock pins the Go kernel's clock for one test.

// freezeClock pins the Go kernel's clock for one test.
func freezeClock(t *testing.T) {
	t.Helper()
	moment, err := time.Parse("2006-01-02T15:04:05.000000Z", frozenMoment)
	if err != nil {
		t.Fatal(err)
	}
	previous := timeNow
	timeNow = func() time.Time { return moment }
	t.Cleanup(func() { timeNow = previous })
}

// runPythonGateStatus runs the Python kernel with its clock pinned to the same
// moment, so both sides render an identical body.

const fixtureTask = "TASK-2"

// plannedProject copies the template into a fresh directory for one case.

func copyTree(source, destination string) error {
	return filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return nil // the fixture holds no links; anything else is not ours to copy
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(source, target string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

// decidableProject is a project with assigned authorities and one planned
// task, which is the least a decision needs to exist at all.

func mutateJSON(t *testing.T, path string, mutate func(map[string]any)) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	mutate(document)
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
}

func rewriteJSON(path string, mutate func(map[string]any)) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return err
	}
	mutate(document)
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(encoded, '\n'), 0o644)
}

func truncate(text string) string {
	if len(text) > 300 {
		return text[:300] + "..."
	}
	return text
}

// goConfiguration runs the Go configuration half.

// writeForgeMock writes one mock file and points an environment variable at it.
func writeForgeMock(t *testing.T, variable string, payload any) {
	t.Helper()
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), strings.ToLower(variable)+".json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(variable, path)
}

// gitHubReviewerMock is the baseline: an open PR authored by the engineering
// lead, with one reviewer already requested and one review already in.

// initProject builds a fresh project overlay with the real kernel.
func initProject(t *testing.T) (root, manifest string) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is unavailable, so there is nothing to compare against")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	manifest = filepath.Join(repoRoot, "provider", "provider.json")
	if _, err := os.Stat(manifest); err != nil {
		t.Skip("no provider manifest in this checkout")
	}

	root = t.TempDir()
	// Built in-process by this kernel. It was Python's, which meant a checkout
	// without Python skipped every test downstream of it rather than failing
	// -- the same defect the planned-project fixture had.
	var output bytes.Buffer
	if code := Run([]string{"--provider", manifest, "init", "--root", root,
		"--profile", "secure-cloud", "--project-id", "probe"}, &output, &output); code != 0 {
		t.Fatalf("building the fixture failed: %s", truncate(output.String()))
	}
	return root, manifest
}

// initialisedProject builds a bare project overlay -- no planned task -- and
// returns a fresh copy of it.
func initialisedProject(t *testing.T) (root, manifest string) {
	t.Helper()
	template, manifest := plannedProjectTemplate(t)
	root = t.TempDir()
	if err := copyTree(template, root); err != nil {
		t.Fatal(err)
	}
	// The shared template already has a planned TASK-2; these cases plan
	// TASK-1 beside it, which also means every case runs against a project
	// that already contains a run rather than an empty one.
	return root, manifest
}

var (
	fixtureOnce     sync.Once
	fixtureTemplate string
	fixtureManifest string
	fixtureSkip     string
)

// plannedProjectTemplate builds one project with a planned, part-approved task
// and returns it for copying. Built once: it costs four kernel invocations,
// and every case wants the same starting point.
func plannedProjectTemplate(t *testing.T) (template, manifest string) {
	t.Helper()
	fixtureOnce.Do(func() {
		root, manifestPath, reason := buildPlannedProject()
		fixtureTemplate, fixtureManifest, fixtureSkip = root, manifestPath, reason
	})
	if fixtureSkip != "" {
		t.Skip(fixtureSkip)
	}
	return fixtureTemplate, fixtureManifest
}

func buildPlannedProject() (root, manifest, skip string) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		return "", "", err.Error()
	}
	manifest = filepath.Join(repoRoot, "provider", "provider.json")
	if _, err := os.Stat(manifest); err != nil {
		return "", "", "no provider manifest in this checkout"
	}
	// Not t.TempDir: this outlives the test that happened to build it.
	root, err = os.MkdirTemp("", "kernel-run-fixture-")
	if err != nil {
		return "", "", err.Error()
	}

	// Built by *this* kernel, not the Python one.
	//
	// It was Python's until the port finished, and that had a failure mode
	// worth naming: when Python could not run, this returned a skip reason and
	// every differential downstream of the fixture reported PASS having
	// executed nothing. A green suite that checks nothing is worse than a red
	// one. Building it in-process removes the skip entirely -- the fixture is
	// now as available as the code under test.
	//
	// It is a starting state, not an assertion. Both kernels then act on
	// identical copies of it, so which one produced it does not privilege
	// either; what it does mean is that the differentials now start from a
	// state the shipping kernel actually produces.
	run := func(args ...string) (int, string) {
		var output bytes.Buffer
		code := Run(append([]string{"--provider", manifest}, args...), &output, &output)
		return code, output.String()
	}
	if code, output := run("init", "--root", root, "--profile", "secure-cloud",
		"--project-id", "probe"); code != 0 {
		return "", "", "kernel init failed: " + truncate(output)
	}

	// Assign every authority. A freshly initialised project has none, and an
	// unassigned authority cannot approve -- so without this the fixture could
	// never reach the state the interesting checks guard.
	authoritiesPath := filepath.Join(root, Overlay, "authorities.json")
	if err := rewriteJSON(authoritiesPath, func(document map[string]any) {
		for role, raw := range document {
			authority, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if _, isAuthority := authority["status"]; !isAuthority {
				continue
			}
			authority["status"] = "assigned"
			authority["assignee"] = "github.com/" + strings.ReplaceAll(role, "_", "-")
			if authority["applicability"] == "unknown" {
				authority["applicability"] = "applicable"
			}
		}
	}); err != nil {
		return "", "", err.Error()
	}

	// Planned after assignment, so the run record's authority requirements are
	// applicable rather than unresolved.
	if code, output := run("plan", "--root", root, "--task-id", fixtureTask,
		"--task", "add an endpoint"); code != 0 {
		return "", "", "kernel plan failed: " + truncate(output)
	}
	if code, output := run("decide", "--root", root, "--task-id", fixtureTask,
		"--gate", "G1", "--role", "product_owner", "--decision", "approved",
		"--actor-id", "github.com/product-owner",
		"--evidence-uri", "github-review:acme/app:pull/1:review/1:reviewer/product-owner",
	); code != 0 {
		return "", "", "kernel decide failed: " + truncate(output)
	}
	return root, manifest, ""
}

// plannedProject copies the template into a fresh directory for one case.
func plannedProject(t *testing.T) (root, manifest string) {
	t.Helper()
	template, manifest := plannedProjectTemplate(t)
	root = t.TempDir()
	if err := copyTree(template, root); err != nil {
		t.Fatalf("copying the fixture: %v", err)
	}
	return root, manifest
}

// TestThePythonKernelIsAvailableToCompareAgainst fails, rather than skips,
// when the other half of the differentials cannot run.
//
// Every test in this package that compares against the Python kernel skips
// when it is unimportable -- which is right for each of them individually and
// wrong for the suite: a run with no Python reports green having compared
// nothing. That is the state this test exists to make visible, and it is not
// hypothetical. Before the shared fixture was built in-process, a missing
// Python turned 43 subtests into silent passes.
//
// This states an existing requirement rather than adding one: the repository's
// own roster/ and plugin/ suites are Python, so a checkout without it cannot
// run its tests anyway. When the Python kernel is deleted, this test and the
// differentials go together -- it is not a reason to keep either.

// decidableProject is a project with assigned authorities and one planned
// task, which is the least a decision needs to exist at all.
func decidableProject(t *testing.T) (root, manifest string) {
	t.Helper()
	template, manifest := plannedProjectTemplate(t)
	root = t.TempDir()
	if err := copyTree(template, root); err != nil {
		t.Fatal(err)
	}
	// The template's own planned task is TASK-2, already carrying a G1
	// decision. These cases want an untouched one.
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Plan(PlanRequest{
		Root: root, TaskID: decideTask, Task: "add an endpoint to the billing service",
	}); err != nil {
		t.Fatalf("planning the fixture task: %v", err)
	}
	return root, manifest
}

// The invariants, stated without reference to the Python kernel so they
// survive its removal.

// makeGateApprovable fills in everything a gate needs before an approval can
// take effect: bound artifacts, evidence, a preparer, and an independent
// verifier who declared they did not prepare it.
func makeGateApprovable(t *testing.T, root string) {
	t.Helper()
	mutateJSON(t, filepath.Join(root, Overlay, "runs", decideTask, "run-record.json"),
		func(document map[string]any) {
			gates, _ := document["lifecycle_gates"].([]any)
			gate, _ := gates[0].(map[string]any)
			gate["evidence_refs"] = []any{map[string]any{
				"evidence_id": "g1-intent", "uri": "github-issue:acme/app:issues/7",
				"hash_algorithm": "sha256", "hash": strings.Repeat("a", 64),
				"classification": "internal",
			}}
			gate["artifact_bindings"] = []any{map[string]any{
				"artifact_id": "intent", "revision": "r1", "digest": "sha256:abc",
			}}
			gate["preparers"] = []any{map[string]any{
				"id": "agent://product-intent-agent", "kind": "agent",
				"role": "product-intent-agent",
			}}
			gate["independent_verifier"] = map[string]any{
				"id": "agent://code-reviewer", "kind": "agent", "role": "code-reviewer",
			}
			gate["independence_declaration"] = map[string]any{
				"verifier_confirmed_not_preparer":   true,
				"verifier_made_material_correction": false,
			}
		})
}

// approveG1For records an approval through the kernel itself, so a case can
// start from a gate that already has one.

// approveG1For records an approval through the kernel itself, so a case can
// start from a gate that already has one.
func approveG1For(t *testing.T, root, manifest string) {
	t.Helper()
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Decide(DecideRequest{
		Root: root, TaskID: decideTask, GateID: "G1", AuthorityRole: "product_owner",
		Decision: "approved", ActorID: decideActor, EvidenceURI: decideEvidence,
		DecidedAt: "2026-08-14T09:00:00+00:00",
	}); err != nil {
		t.Fatalf("seeding an approval: %v", err)
	}
}

// statusProject is a project with one planned task to report on.
func statusProject(t *testing.T) (root, manifest string) {
	t.Helper()
	return decidableProject(t)
}

// reviewerFixture builds a project with assigned authorities, forge bindings,
// and one planned task.
func reviewerFixture(t *testing.T) (root, manifest string) {
	t.Helper()
	root, manifest = decidableProject(t)
	mutateJSON(t, filepath.Join(root, Overlay, "authorities.json"),
		func(document map[string]any) {
			for role, raw := range document {
				authority, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				if _, isAuthority := authority["status"]; !isAuthority {
					continue
				}
				// A GitLab binding beside the GitHub one, so the same fixture
				// drives both reports.
				authority["gitlab_username"] = strings.ReplaceAll(role, "_", "-")
			}
		})
	return root, manifest
}

// writeForgeMock writes one mock file and points an environment variable at it.

// githubBoundProject binds the product owner to a GitHub login.
func githubBoundProject(t *testing.T) (root, manifest string) {
	t.Helper()
	root, manifest = decidableProject(t)
	makeGateApprovable(t, root)
	mutateJSON(t, filepath.Join(root, Overlay, "authorities.json"),
		func(document map[string]any) {
			owner, _ := document["product_owner"].(map[string]any)
			owner["github_login"] = "product-owner"
		})
	return root, manifest
}

// gitlabBoundProject binds the product owner to a GitLab username.

// gitlabBoundProject binds the product owner to a GitLab username.
func gitlabBoundProject(t *testing.T) (root, manifest string) {
	t.Helper()
	root, manifest = decidableProject(t)
	makeGateApprovable(t, root)
	mutateJSON(t, filepath.Join(root, Overlay, "authorities.json"),
		func(document map[string]any) {
			owner, _ := document["product_owner"].(map[string]any)
			owner["gitlab_username"] = "product-owner"
		})
	return root, manifest
}

// gitHubReviewerMock is the baseline: an open PR authored by the engineering
// lead, with one reviewer already requested and one review already in.
func gitHubReviewerMock() map[string]any {
	collaborators := map[string]any{}
	users := map[string]any{}
	for _, role := range AuthorityRoleOrder {
		login := strings.ReplaceAll(role, "_", "-")
		users[login] = true
		collaborators["acme/app:"+login] = true
	}
	return map[string]any{
		"identity": map[string]any{"login": "sdlc-bot"},
		"pr": map[string]any{
			"number": 3, "state": "open", "draft": false, "merged": false,
			"head": map[string]any{"sha": "abc123"},
			"base": map[string]any{"repo": map[string]any{"full_name": "acme/app"}},
			// Somebody who holds no authority, so the baseline is clean and
			// each case below introduces exactly one problem. The
			// pr-author-conflict branch has its own case.
			"user": map[string]any{"login": "outside-contributor"},
		},
		"requested_reviewers": map[string]any{
			"users": []any{map[string]any{"login": "system-architect"}},
		},
		"users": users, "collaborators": collaborators,
	}
}

func gitHubReviewsMock() []any {
	return []any{
		// A review of the current head, and a review of an older commit --
		// already-reviewed and review-stale respectively.
		map[string]any{
			"user": map[string]any{"login": "product-owner"}, "state": "APPROVED",
			"submitted_at": "2026-08-15T09:00:00Z", "commit_id": "abc123",
		},
		map[string]any{
			"user": map[string]any{"login": "governance-lead"}, "state": "APPROVED",
			"submitted_at": "2026-08-14T09:00:00Z", "commit_id": "old999",
		},
	}
}

func gitLabReviewerMock() map[string]any {
	users := map[string]any{}
	for _, role := range AuthorityRoleOrder {
		username := strings.ReplaceAll(role, "_", "-")
		users[username] = []any{
			map[string]any{"id": 1, "username": username, "state": "active"},
		}
	}
	return map[string]any{
		"identity": map[string]any{"username": "sdlc-bot"},
		"mr": map[string]any{
			"iid": 5, "state": "opened", "draft": false, "sha": "def456",
			"references": map[string]any{"full": "acme/app!5"},
			"author":     map[string]any{"username": "engineering-lead"},
			"reviewers":  []any{map[string]any{"username": "system-architect"}},
		},
		"users": users,
	}
}

func review(id int, login, state, submittedAt, commit string) map[string]any {
	return map[string]any{
		"id": id, "state": state, "submitted_at": submittedAt, "commit_id": commit,
		"user": map[string]any{"login": login},
	}
}

// repairableProject builds a project for `repair` to reconcile.
//
// Built by this kernel's own `init` rather than the Python kernel's. The
// original point of using Python's was that repair would be reconciling
// something it did not create -- which still holds, because `init` and
// `repair` are different code paths, and it is the *artifacts* repair must
// treat as decisions rather than the process that made them.
func repairableProject(t *testing.T) (root, manifest string) {
	t.Helper()
	manifest = providerManifest(t)
	if _, err := os.Stat(manifest); err != nil {
		t.Skip("no provider manifest in this checkout")
	}
	root = filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if code := Run([]string{"--provider", manifest, "init", "--root", root,
		"--profile", "secure-cloud", "--project-id", "probe"}, &output, &output); code != 0 {
		t.Fatalf("building the fixture failed: %s", truncate(output.String()))
	}
	return root, manifest
}

// providerManifest is the provider this repository ships, which the fixtures
// are built against.
func providerManifest(t *testing.T) string {
	t.Helper()
	return filepath.Join(repositoryRoot(t), "provider", "provider.json")
}
