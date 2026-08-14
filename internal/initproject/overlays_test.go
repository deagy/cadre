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
