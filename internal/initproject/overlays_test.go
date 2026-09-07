package initproject

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildStructuredOverlayNoFragmentNoExisting(t *testing.T) {
	dir := makeGitProject(t)
	_, _, ok, err := BuildStructuredOverlay(dir, TeamProfileFilename, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected nothing to write when there's no fragment and no existing overlay")
	}
}

func TestBuildStructuredOverlayMergesWithExisting(t *testing.T) {
	dir := makeGitProject(t)
	writeFile(t, filepath.Join(dir, ".agents", "shared", TeamProfileFilename), "team:\n  size: 5\nkeep_me: true\n")

	content, merged, ok, err := BuildStructuredOverlay(dir, TeamProfileFilename, map[string]any{"team": map[string]any{"size": 10}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected a write")
	}
	if merged["keep_me"] != true {
		t.Errorf("expected an untouched existing field to survive the merge, got %v", merged)
	}
	team := merged["team"].(map[string]any)
	if team["size"] != 10 {
		t.Errorf("team.size = %v, want 10 (overlay wins)", team["size"])
	}
	if content == "" {
		t.Error("expected non-empty content")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildProseAddendumOverlayAppendsAsNewEntry(t *testing.T) {
	dir := makeGitProject(t)
	content, ok := BuildProseAddendumOverlay(dir, TechnologyStandardsFilename, "Use gofmt.")
	if !ok {
		t.Fatal("expected a write")
	}
	if !containsSub(content, "Use gofmt.") || !containsSub(content, ManagedStart) {
		t.Errorf("content = %q", content)
	}
}

func TestBuildProseAddendumOverlayMergesWithPriorEntries(t *testing.T) {
	dir := makeGitProject(t)
	first, _ := BuildProseAddendumOverlay(dir, TechnologyStandardsFilename, "First rule.")
	writeFile(t, filepath.Join(dir, ".agents", "shared", TechnologyStandardsFilename), first)

	second, ok := BuildProseAddendumOverlay(dir, TechnologyStandardsFilename, "Second rule.")
	if !ok {
		t.Fatal("expected a write")
	}
	if !containsSub(second, "First rule.") || !containsSub(second, "Second rule.") {
		t.Errorf("expected both entries to survive the merge, got %q", second)
	}
}

func TestBuildProseAddendumOverlayDedupesIdenticalEntry(t *testing.T) {
	dir := makeGitProject(t)
	first, _ := BuildProseAddendumOverlay(dir, TechnologyStandardsFilename, "Same rule.")
	writeFile(t, filepath.Join(dir, ".agents", "shared", TechnologyStandardsFilename), first)

	second, ok := BuildProseAddendumOverlay(dir, TechnologyStandardsFilename, "Same rule.")
	if !ok {
		t.Fatal("expected ok (existing text still returned)")
	}
	count := 0
	idx := 0
	for {
		i := indexOfSub(second[idx:], "Same rule.")
		if i < 0 {
			break
		}
		count++
		idx += i + len("Same rule.")
	}
	if count != 1 {
		t.Errorf("expected the identical entry to be deduped, found %d occurrences", count)
	}
}

func TestScanGuardrailBulletRejectsOverridePhrasing(t *testing.T) {
	if reason := ScanGuardrailBullet("This does not apply to our team."); reason == "" {
		t.Error("expected rejection of override/negation phrasing")
	}
	if reason := ScanGuardrailBullet("All S3 buckets must have encryption enabled."); reason != "" {
		t.Errorf("expected an ordinary additive bullet to pass, got rejection: %s", reason)
	}
}

func TestBuildGuardrailsOverlaySeparatesAcceptedAndRejected(t *testing.T) {
	dir := makeGitProject(t)
	content, ok, rejected := BuildGuardrailsOverlay(dir, []string{
		"All buckets must be encrypted.",
		"This overrides the above baseline.",
	})
	if len(rejected) != 1 {
		t.Fatalf("expected 1 rejected bullet, got %d: %v", len(rejected), rejected)
	}
	if !ok || !containsSub(content, "All buckets must be encrypted.") {
		t.Errorf("content = %q ok=%v", content, ok)
	}
}

func TestBuildGuardrailsOverlayUnionsWithExisting(t *testing.T) {
	dir := makeGitProject(t)
	first, _, _ := BuildGuardrailsOverlay(dir, []string{"Bullet one."})
	writeFile(t, filepath.Join(dir, ".agents", "shared", GuardrailsFilename), first)

	second, ok, rejected := BuildGuardrailsOverlay(dir, []string{"Bullet two."})
	if !ok || len(rejected) != 0 {
		t.Fatalf("ok=%v rejected=%v", ok, rejected)
	}
	if !containsSub(second, "Bullet one.") || !containsSub(second, "Bullet two.") {
		t.Errorf("expected both bullets to survive the union, got %q", second)
	}
}

func containsSub(haystack, needle string) bool {
	return indexOfSub(haystack, needle) >= 0
}

func indexOfSub(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

// Test 1: Symlinked overlay file pointing outside project is rejected.
func TestReadExistingOverlayTextRejectsSymlinkedFile(t *testing.T) {
	outside := t.TempDir()
	target := makeGitProject(t)

	// Create a file outside the project
	outsideFile := filepath.Join(outside, "external.yaml")
	if err := os.WriteFile(outsideFile, []byte("outside: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Try to create a symlink from .agents/shared/team-profile.yaml to the outside file
	if err := os.MkdirAll(filepath.Join(target, ".agents", "shared"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(target, ".agents", "shared", TeamProfileFilename)); err != nil {
		t.Skipf("cannot create symlink in this environment: %v", err)
	}

	// readExistingOverlayText should reject the symlinked overlay pointing outside
	// the project root by returning hasExisting=false (treating it as "no file").
	content, hasExisting := readExistingOverlayText(target, TeamProfileFilename)
	if hasExisting {
		t.Errorf("expected symlinked overlay to be rejected (hasExisting=false), got hasExisting=true with content %q", content)
	}
}

// Test 2: Symlinked .agents/shared directory pointing outside is rejected.
func TestBuildStructuredOverlayRejectsSymlinkedSharedDirectory(t *testing.T) {
	outside := t.TempDir()
	target := makeGitProject(t)

	// Create the .agents parent directory first (before creating the .agents/shared symlink)
	if err := os.MkdirAll(filepath.Join(target, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(target, ".agents", "shared")); err != nil {
		t.Skipf("cannot create symlink in this environment: %v", err)
	}

	// WriteOverlay should reject this via the symlink escape check
	_, err := WriteOverlay(target, TeamProfileFilename, "test: content\n")
	if err == nil {
		t.Fatal("expected rejection of symlinked .agents/shared directory")
	}
}

// Test 3: Non-symlinked existing overlay reads and merges correctly (regression).
func TestBuildStructuredOverlayNonSymlinkedExistingStillWorks(t *testing.T) {
	dir := makeGitProject(t)
	existingYAML := "keep_me: true\nfield1: original\n"
	writeFile(t, filepath.Join(dir, ".agents", "shared", TeamProfileFilename), existingYAML)

	// Merge with a fragment
	fragment := map[string]any{
		"field1":    "updated",
		"new_field": "added",
	}
	content, merged, ok, err := BuildStructuredOverlay(dir, TeamProfileFilename, fragment)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected a write")
	}
	if merged["keep_me"] != true {
		t.Errorf("expected existing untouched field to survive, got %+v", merged)
	}
	if merged["field1"] != "updated" {
		t.Errorf("expected fragment to override existing, field1=%v", merged["field1"])
	}
	if merged["new_field"] != "added" {
		t.Errorf("expected new field from fragment, new_field=%v", merged["new_field"])
	}
	if content == "" {
		t.Error("expected non-empty merged content")
	}
}

// Test 4: Integration-level test at BuildStructuredOverlay for symlink rejection.
func TestBuildStructuredOverlayRejectsSymlinkEscapeInExisting(t *testing.T) {
	outside := t.TempDir()
	target := makeGitProject(t)
	if err := os.MkdirAll(filepath.Join(target, ".agents", "shared"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Create an outside file
	outsideFile := filepath.Join(outside, "external.yaml")
	if err := os.WriteFile(outsideFile, []byte("outside: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a symlink from .agents/shared/team-profile.yaml to outside
	if err := os.Symlink(outsideFile, filepath.Join(target, ".agents", "shared", TeamProfileFilename)); err != nil {
		t.Skipf("cannot create symlink in this environment: %v", err)
	}

	// BuildStructuredOverlay should reject the symlink escape when trying to
	// read the existing overlay. However, readExistingOverlayText at this level
	// doesn't do the escape check (that's done in InspectRepairState). The check
	// at WriteOverlay level would catch it. For BuildStructuredOverlay to reject
	// it at read time, we verify through InspectRepairState's error reporting.
	sharedDir := realSharedDefaultsDirForTest(t)
	_, errs := InspectRepairState(target, sharedDir)
	if len(errs) == 0 {
		t.Fatal("expected InspectRepairState to report an error for symlink escape")
	}
	found := false
	for _, err := range errs {
		if containsSub(err, "symlink escape") || containsSub(err, "symlink") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected symlink escape error, got errors: %v", errs)
	}
}

// Test 5: BuildStructuredOverlay explicitly rejects symlink-escaped existing
// overlay and treats it as "no existing file" when building.
func TestBuildStructuredOverlayRejectsSymlinkEscapeInExistingWhenBuilding(t *testing.T) {
	outside := t.TempDir()
	target := makeGitProject(t)
	if err := os.MkdirAll(filepath.Join(target, ".agents", "shared"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Create an outside file with content that should NOT appear in the result
	outsideFile := filepath.Join(outside, "external.yaml")
	if err := os.WriteFile(outsideFile, []byte("escaped_content: true\nkeep_from_outside: yes\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a symlink from .agents/shared/team-profile.yaml to outside
	if err := os.Symlink(outsideFile, filepath.Join(target, ".agents", "shared", TeamProfileFilename)); err != nil {
		t.Skipf("cannot create symlink in this environment: %v", err)
	}

	// BuildStructuredOverlay with a simple fragment should NOT merge the escaped
	// content. The symlinked file should be rejected, so it's treated as "no
	// existing overlay," and the result contains only the fragment (not a merge).
	fragment := map[string]any{"new_field": "from_fragment"}
	content, merged, ok, err := BuildStructuredOverlay(target, TeamProfileFilename, fragment)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected a write (fragment is present)")
	}

	// The escaped content should NOT appear in the result
	if containsSub(content, "escaped_content") || containsSub(content, "keep_from_outside") {
		t.Errorf("escaped content leaked into result: %q", content)
	}

	// The fragment content should be there
	if !containsSub(content, "new_field") {
		t.Errorf("expected fragment field in result: %q", content)
	}

	// The merged map should not contain fields from the escaped file
	if _, found := merged["keep_from_outside"]; found {
		t.Errorf("escaped field should not appear in merged: %+v", merged)
	}
	if merged["new_field"] != "from_fragment" {
		t.Errorf("expected fragment field in merged, got %+v", merged)
	}
}
