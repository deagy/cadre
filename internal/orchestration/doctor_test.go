package orchestration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeCheckout creates a temp directory with real Cadre checkout markers.
func makeCheckout(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "roster"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "roster", "catalog.yaml"), []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "cadre"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestRepoMarkersPresent(t *testing.T) {
	root := makeCheckout(t)
	if !repoMarkersPresent(root) {
		t.Fatalf("expected markers present at %s", root)
	}
	if repoMarkersPresent(t.TempDir()) {
		t.Fatalf("expected no markers present at an empty temp dir")
	}
}

func TestFindCheckoutRoot(t *testing.T) {
	root := makeCheckout(t)
	nested := filepath.Join(root, "internal", "cli")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	found := FindCheckoutRoot(nested)
	if found != root {
		t.Fatalf("FindCheckoutRoot(%s) = %q, want %q", nested, found, root)
	}

	if got := FindCheckoutRoot(t.TempDir()); got != "" {
		t.Fatalf("FindCheckoutRoot on a non-checkout dir = %q, want empty", got)
	}
}

func TestClassifyRunningBinaryCheckout(t *testing.T) {
	root := makeCheckout(t)
	cacheDir := filepath.Join(root, ".cadre-build-cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(cacheDir, "cadre")

	kind, installRoot, detail := ClassifyRunningBinary(binary)
	if kind != InstallKindCheckout {
		t.Fatalf("kind = %q, want %q", kind, InstallKindCheckout)
	}
	if installRoot != root {
		t.Fatalf("installRoot = %q, want %q", installRoot, root)
	}
	if detail == "" {
		t.Fatal("expected non-empty detail")
	}
}

func TestClassifyRunningBinaryUnknown(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "cadre")

	kind, installRoot, _ := ClassifyRunningBinary(binary)
	if kind != InstallKindUnknown {
		t.Fatalf("kind = %q, want %q", kind, InstallKindUnknown)
	}
	if installRoot != dir {
		t.Fatalf("installRoot = %q, want %q", installRoot, dir)
	}
}

func TestClassifyRunningBinaryPluginCache(t *testing.T) {
	dir := t.TempDir()
	pluginPath := filepath.Join(dir, "plugins", "cache", "deagy", "cadre-lifecycle", "v1.2.3", "bin", "cadre")

	kind, installRoot, detail := ClassifyRunningBinary(pluginPath)
	if kind != InstallKindPluginCache {
		t.Fatalf("kind = %q, want %q", kind, InstallKindPluginCache)
	}
	wantRoot := filepath.ToSlash(filepath.Join(dir, "plugins", "cache", "deagy", "cadre-lifecycle", "v1.2.3"))
	if installRoot != wantRoot {
		t.Fatalf("installRoot = %q, want %q", installRoot, wantRoot)
	}
	if detail == "" {
		t.Fatal("expected non-empty detail")
	}
}

func TestClassifyRunningBinaryGoInstall(t *testing.T) {
	dir := t.TempDir()
	gobin := filepath.Join(dir, "gobin")
	if err := os.MkdirAll(gobin, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOBIN", gobin)
	binary := filepath.Join(gobin, "cadre")

	kind, installRoot, _ := ClassifyRunningBinary(binary)
	if kind != InstallKindGoInstall {
		t.Fatalf("kind = %q, want %q", kind, InstallKindGoInstall)
	}
	if installRoot != gobin {
		t.Fatalf("installRoot = %q, want %q", installRoot, gobin)
	}
}

func TestGatherDoctorReportNoMismatch(t *testing.T) {
	root := makeCheckout(t)
	cacheDir := filepath.Join(root, ".cadre-build-cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(cacheDir, "cadre")

	report := GatherDoctorReport(root, binary)
	if report.Mismatch {
		t.Fatalf("expected no mismatch, got: %s", report.MismatchDetail)
	}
	if report.InstallKind != InstallKindCheckout {
		t.Fatalf("InstallKind = %q, want %q", report.InstallKind, InstallKindCheckout)
	}
	if report.CWDCheckoutRoot != root {
		t.Fatalf("CWDCheckoutRoot = %q, want %q", report.CWDCheckoutRoot, root)
	}
}

func TestGatherDoctorReportMismatch(t *testing.T) {
	root := makeCheckout(t)
	otherDir := t.TempDir()
	binary := filepath.Join(otherDir, "cadre")

	report := GatherDoctorReport(root, binary)
	if !report.Mismatch {
		t.Fatal("expected a mismatch when the running binary is outside the cwd's checkout")
	}
	if report.MismatchDetail == "" {
		t.Fatal("expected a non-empty mismatch detail")
	}
}

func TestGatherDoctorReportCWDNotInCheckout(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "cadre")

	report := GatherDoctorReport(dir, binary)
	if report.Mismatch {
		t.Fatal("expected no mismatch when cwd is not inside any checkout at all")
	}
	if report.CWDCheckoutRoot != "" {
		t.Fatalf("CWDCheckoutRoot = %q, want empty", report.CWDCheckoutRoot)
	}
}

func TestRenderDoctorReport(t *testing.T) {
	report := DoctorReport{
		RunningBinary: "/tmp/cadre",
		GoVersion:     "go1.25.1",
		GoVersionOK:   true,
		GoMinVersion:  MinGoVersion,
		InstallKind:   InstallKindUnknown,
		InstallRoot:   "/tmp",
		InstallDetail: "some detail",
		CWD:           "/home/user/project",
	}
	out := RenderDoctorReport(report)
	if out == "" {
		t.Fatal("expected non-empty rendered output")
	}
	if !contains(out, "OK:") {
		t.Fatalf("expected an OK line in output without a mismatch:\n%s", out)
	}

	report.Mismatch = true
	report.MismatchDetail = "something is wrong"
	out = RenderDoctorReport(report)
	if !contains(out, "WARNING: something is wrong") {
		t.Fatalf("expected mismatch warning in output:\n%s", out)
	}
}

// TestRenderDoctorReportKnowledgeStore pins all three states of the
// knowledge-store line, including the one that is easy to get wrong: an
// unprobed report must stay silent rather than render as a failure. The
// probe lives in internal/cli (this package has no sqlite dependency), so a
// zero-valued DoctorReport reaching the renderer is a normal occurrence, not
// a bug -- and reporting "unavailable" for it would be a false alarm.
func TestRenderDoctorReportKnowledgeStore(t *testing.T) {
	base := DoctorReport{
		RunningBinary: "/tmp/cadre",
		GoVersion:     "go1.25.1",
		GoVersionOK:   true,
		GoMinVersion:  MinGoVersion,
		InstallKind:   InstallKindUnknown,
		InstallRoot:   "/tmp",
		InstallDetail: "some detail",
		CWD:           "/home/user/project",
	}

	unprobed := RenderDoctorReport(base)
	if contains(unprobed, "knowledge store:") {
		t.Fatalf("an unprobed report must not render a knowledge-store line:\n%s", unprobed)
	}
	if contains(unprobed, "built without cgo") {
		t.Fatalf("an unprobed report must not warn about cgo:\n%s", unprobed)
	}

	available := base
	available.KnowledgeStoreOK = true
	available.KnowledgeStoreDetail = "available (pure-Go sqlite driver, no cgo required)"
	out := RenderDoctorReport(available)
	if !contains(out, "knowledge store:    available") {
		t.Fatalf("expected the available line:\n%s", out)
	}
	if contains(out, "will fail at runtime") {
		t.Fatalf("must not warn when the driver is available:\n%s", out)
	}

	degraded := base
	degraded.KnowledgeStoreOK = false
	degraded.KnowledgeStoreDetail = "unavailable -- stub driver"
	out = RenderDoctorReport(degraded)
	if !contains(out, "knowledge store:    unavailable -- stub driver") {
		t.Fatalf("expected the unavailable line:\n%s", out)
	}
	if !contains(out, "will fail at runtime") {
		t.Fatalf("expected the driver warning when it is unavailable:\n%s", out)
	}
	// The warning must not blame cgo. The knowledge path is pure Go, so a
	// caller sent to install a C toolchain would be chasing the wrong thing.
	if contains(out, "built without cgo") {
		t.Fatalf("the warning still blames cgo for a pure-Go driver:\n%s", out)
	}
}

func TestGoVersionOK(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{"go1.25.1", true},
		{"go1.26.0", true},
		{"go2.0.0", true},
		{"go1.24.0", false},
		{"go1.9.0", false},
		{"devel +abcdef1234", true}, // Development toolchain build -- assume newer.
	}
	for _, tt := range tests {
		if got := goVersionOK(tt.version); got != tt.want {
			t.Errorf("goVersionOK(%q) = %v, want %v", tt.version, got, tt.want)
		}
	}
}

// contains previously lived in dispatch_plan_test.go, removed with the
// divergent dispatch-plan builder. Kept here because doctor_test.go is the
// remaining user.
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

func TestClassifyRunningBinaryPipInstall(t *testing.T) {
	// doctor exists to tell an operator which install answered the command,
	// and README.md promises it names a pip/pipx install among the kinds.
	// The Go port classified every pip and pipx path as "unknown" -- the most
	// common install there is, reported as unclassifiable.
	//
	// Both shapes, because a wheel that ships a binary can land either way:
	// under site-packages for a plain pip install, or in a pipx venv's bin/,
	// which is not under site-packages at all.
	for _, probe := range []struct {
		path string
		root string
		why  string
	}{
		{
			path: "/home/u/.local/lib/python3.12/site-packages/cadre_cli/bin/cadre",
			root: "/home/u/.local/lib/python3.12/site-packages",
			why:  "a user-site pip install",
		},
		{
			path: "/usr/lib/python3/site-packages/cadre_cli/cadre",
			root: "/usr/lib/python3/site-packages",
			why:  "a system pip install",
		},
		{
			path: "/home/u/.local/pipx/venvs/cadre/bin/cadre",
			root: "/home/u/.local/pipx/venvs/cadre",
			why:  "a pipx install, whose binary is in the venv's bin/",
		},
	} {
		kind, root, detail := ClassifyRunningBinary(probe.path)
		if kind != InstallKindPipInstall {
			t.Errorf("%s: kind = %q, want %q (%s)", probe.path, kind, InstallKindPipInstall, probe.why)
		}
		if root != probe.root {
			t.Errorf("%s: root = %q, want %q", probe.path, root, probe.root)
		}
		if detail == "" {
			t.Errorf("%s: no detail reported", probe.path)
		}
	}
}

func TestAPathThatIsNotAnInstallIsStillReportedAsUnknown(t *testing.T) {
	// The honest answer has to survive. A classifier that stretched to fit
	// would name an install kind for a path that is not one, which is worse
	// than saying so: doctor's whole value is that its answer can be trusted.
	kind, _, detail := ClassifyRunningBinary("/opt/somewhere/random/cadre")
	if kind != InstallKindUnknown {
		t.Errorf("kind = %q, want %q", kind, InstallKindUnknown)
	}
	if !strings.Contains(detail, "could not classify") {
		t.Errorf("detail = %q, want it to say the classification failed", detail)
	}
}

func TestAMismatchIsReportedForADifferentCheckout(t *testing.T) {
	// The subtle mismatch: same install *kind*, different root. The cwd is
	// inside one checkout and the binary that ran belongs to another, which
	// is exactly the situation where an operator edits one tree and tests
	// another -- and the one a kind-only comparison would miss.
	cwdCheckout := t.TempDir()
	otherCheckout := t.TempDir()
	for _, root := range []string{cwdCheckout, otherCheckout} {
		for _, marker := range []string{".git", "roster"} {
			if err := os.MkdirAll(filepath.Join(root, marker), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(root, "roster", "catalog.yaml"), []byte("agents: {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "bin", "cadre"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	report := GatherDoctorReport(cwdCheckout, filepath.Join(otherCheckout, "bin", "cadre"))
	if !report.Mismatch {
		t.Error("a binary from a different checkout was not reported as a mismatch")
	}
}
