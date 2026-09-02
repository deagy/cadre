package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The roster's governance documents are read by agents and operators as
// statements of what this CLI does. When one names a verb the CLI answers
// with a generic "unknown subcommand", the document sends its reader to a
// dead end -- and a policy document describing, say, steward-only deletion
// with named authorization becomes a compliance claim with no implementation
// behind it, which is worse than a missing feature because reading it reveals
// nothing.
//
// Six verbs were in exactly that state when this test was written:
// `context`, `retention-report`, `delete-ingested`, `list-staged`,
// `export-staged` and `deletion-evidence`, all documented, none built here,
// none acknowledged. They are answered by name now. This is what stops the
// next six.
//
// The check is deliberately not "the docs are correct" -- it cannot be. It is
// the narrower, mechanical property that every verb a document names is one
// the CLI will explain, whether by running, by naming its replacement, or by
// saying it was never built.

// documentedVerb is one `cadre knowledge <verb>` mention in a document.
type documentedVerb struct {
	verb string
	file string
	line int
}

var (
	// knowledgeVerbPattern matches `cadre knowledge <verb>`.
	knowledgeVerbPattern = regexp.MustCompile(`^cadre knowledge ([a-z][a-z0-9-]*)`)
	// topLevelVerbPattern matches `cadre <verb>`.
	topLevelVerbPattern = regexp.MustCompile(`^cadre ([a-z][a-z0-9-]*)`)
	// codeSpanPattern extracts backtick-delimited spans.
	codeSpanPattern = regexp.MustCompile("`([^`\n]+)`")
)

// commandSegments yields the parts of a line where a document is naming a
// command rather than talking about the tool.
//
// Backtick spans and fenced-block lines only. Matching bare prose would flag
// "the cadre binary" and "a cadre role" as verbs, and a guard that cries wolf
// on English gets suppressed rather than fixed.
func commandSegments(line string, inFence bool) []string {
	if inFence {
		return []string{strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "$ "))}
	}
	var segments []string
	for _, match := range codeSpanPattern.FindAllStringSubmatch(line, -1) {
		segments = append(segments, strings.TrimSpace(strings.TrimPrefix(match[1], "$ ")))
	}
	return segments
}

// scanMarkdownForVerbs extracts every `cadre <verb>` mention in one file.
//
// Split out of the walker so the repository root can be scanned without
// recursing into it: the root holds hand-authored documents beside every
// generated and vendored tree in the project.
func scanMarkdownForVerbs(path string) ([]documentedVerb, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return scanMarkdownLines(path, strings.Split(string(content), "\n")), nil
}

// scanMarkdownLines is the shared extraction, over lines a caller may have
// already trimmed.
func scanMarkdownLines(path string, lines []string) []documentedVerb {
	var found []documentedVerb
	inFence := false
	for index, text := range lines {
		if strings.HasPrefix(strings.TrimSpace(text), "```") {
			inFence = !inFence
			continue
		}
		for _, segment := range commandSegments(text, inFence) {
			if match := knowledgeVerbPattern.FindStringSubmatch(segment); match != nil {
				found = append(found, documentedVerb{
					verb: "knowledge " + match[1], file: path, line: index + 1})
				continue
			}
			if match := topLevelVerbPattern.FindStringSubmatch(segment); match != nil {
				found = append(found, documentedVerb{
					verb: match[1], file: path, line: index + 1})
			}
		}
	}
	return found
}

// linesBeforeDecisionLog truncates a document at its Decision Log heading.
//
// Same principle that keeps roster/orchestration/runs/ and the CHANGELOG's
// dated releases out of this guard: a decision log is a record of what was
// believed and decided on a date, and a command named in one was real when
// it was written. ADR-001's log names an undocumented `cadre execute` found
// during a refactor -- and resolves it three lines later, "Resolved
// 2026-08-14 by removal." Rewriting that would falsify the record to satisfy
// a guard, which is the opposite of what the guard is for. The document
// body above the log is a live claim and is still scanned.
func linesBeforeDecisionLog(lines []string) []string {
	for index, text := range lines {
		trimmed := strings.TrimSpace(text)
		if strings.HasPrefix(trimmed, "#") && strings.Contains(strings.ToLower(trimmed), "decision log") {
			return lines[:index]
		}
	}
	return lines
}

// scanRepoRootMarkdown reads the hand-authored .md files at the repository
// root, without recursing.
//
// This root is not an afterthought either. CP-4 on the capability-parity
// ultragoal found that the two worst stale-capability documents in the
// project -- RELEASE_NOTES_PHASE4.md, announcing retention and deletion as
// COMPLETE and Production Ready, and PHASE4_ROADMAP.md -- sat here, outside
// every scan root this guard had. Both were corrected by hand, and a
// phantom verb injected into either one still passed this test, while the
// same injection into roster/ failed. A correction with no guard behind it
// is exactly what this criterion exists to prevent, so the guard now
// reaches the files it was written about.
//
// CHANGELOG.md is excluded here because scanUnreleasedChangelog already
// reads it, and reads only the section that is a claim rather than history.
func scanRepoRootMarkdown(t *testing.T, repo string) []documentedVerb {
	t.Helper()
	entries, err := os.ReadDir(repo)
	if err != nil {
		t.Fatalf("reading %s: %v", repo, err)
	}
	var found []documentedVerb
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		if entry.Name() == "CHANGELOG.md" {
			continue
		}
		path := filepath.Join(repo, entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("scanning %s: %v", entry.Name(), err)
		}
		lines := linesBeforeDecisionLog(strings.Split(string(content), "\n"))
		found = append(found, scanMarkdownLines(path, lines)...)
	}
	return found
}

// scanDocumentedKnowledgeVerbs walks the roster for verbs the documents name.
func scanDocumentedKnowledgeVerbs(t *testing.T, root string) []documentedVerb {
	t.Helper()
	var found []documentedVerb

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// roster/orchestration/runs/ is an archive of past runs: each file
		// records what was believed and decided on a date. A command named
		// there was real when it was written, and "correcting" it would
		// falsify the record rather than fix a document. Excluded on that
		// principle, not for convenience -- it is the same reasoning that
		// kept the P4 migration from sweeping 519 prose mentions.
		if entry.IsDir() {
			if entry.Name() == "runs" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		mentions, err := scanMarkdownForVerbs(path)
		if err != nil {
			return err
		}
		found = append(found, mentions...)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return found
}

// scanUnreleasedChangelog reads only the CHANGELOG's [Unreleased] section.
//
// The split is the point. A dated release section records what was true when
// it shipped -- a command named there was real, and rewriting it would
// falsify the changelog, which is the same reasoning that keeps
// roster/orchestration/runs/ out of this scan. But [Unreleased] is not
// history: it is a claim about what the next release contains, read as
// current. An entry there describing `--source` as repeatable on a verb the
// binary no longer has is a live claim about a command that is gone.
//
// This scan originally skipped the file wholesale as "history". A verifier
// found the [Unreleased] mention, and checking the heading settled it the
// other way.
func scanUnreleasedChangelog(t *testing.T, path string) []documentedVerb {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var found []documentedVerb
	inUnreleased, inFence := false, false
	for index, text := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(text)
		if strings.HasPrefix(trimmed, "## ") {
			// Any dated release heading ends the live section.
			inUnreleased = strings.Contains(strings.ToLower(trimmed), "unreleased")
			continue
		}
		if !inUnreleased {
			continue
		}
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		for _, segment := range commandSegments(text, inFence) {
			if match := knowledgeVerbPattern.FindStringSubmatch(segment); match != nil {
				found = append(found, documentedVerb{
					verb: "knowledge " + match[1], file: path, line: index + 1})
				continue
			}
			if match := topLevelVerbPattern.FindStringSubmatch(segment); match != nil {
				found = append(found, documentedVerb{
					verb: match[1], file: path, line: index + 1})
			}
		}
	}
	return found
}

// TestEveryDocumentedKnowledgeVerbIsAnswerable.
//
// "Answerable" is a low bar on purpose: running, naming a replacement, or
// admitting it was never built all pass. What fails is silence.
func TestEveryDocumentedKnowledgeVerbIsAnswerable(t *testing.T) {
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// Both hand-authored roots. roster/ is the obvious one; .agents/skills/ is
	// the one the first version of this guard missed, and it is not an
	// afterthought -- plugin_generation.go reads it as an input root, and two
	// of its SKILL.md files instruct an agent to run `cadre knowledge
	// context`. A guard scoped to roster/ would have reported parity while
	// live instructions pointed at a dead verb.
	//
	// The generated trees (plugin/, cline-plugins/) are deliberately not
	// scanned: they are outputs of these roots, held current by
	// `generate-plugin --check`, so a mention there is a copy of one here.
	roots := []string{
		filepath.Join(repo, "roster"),
		filepath.Join(repo, ".agents", "skills"),
	}
	root := roots[0]
	if _, err := os.Stat(root); err != nil {
		t.Skipf("no roster/ beside this package: %v", err)
	}

	answerable := map[string]bool{}
	for verb := range AnswerableKnowledgeVerbs() {
		answerable["knowledge "+verb] = true
	}
	// Top-level verbs come from bin/subcommands.tsv, the table `cadre help`
	// and TestEverySubcommandExitsZeroOnHelp already read, so this guard
	// cannot disagree with them about what exists.
	table := filepath.Join(filepath.Dir(root), SubcommandsTableRelativePath)
	subcommands, err := LoadSubcommands(table)
	if err != nil {
		t.Fatalf("loading %s: %v", table, err)
	}
	for _, subcommand := range subcommands {
		answerable[subcommand.Name] = true
	}
	for _, extra := range []string{"help", "knowledge", "sdlc"} {
		answerable[extra] = true
	}

	var documented []documentedVerb
	for _, scanRoot := range roots {
		if _, err := os.Stat(scanRoot); err != nil {
			continue
		}
		documented = append(documented, scanDocumentedKnowledgeVerbs(t, scanRoot)...)
	}
	documented = append(documented, scanRepoRootMarkdown(t, repo)...)
	documented = append(documented, scanUnreleasedChangelog(t, filepath.Join(repo, "CHANGELOG.md"))...)
	if len(documented) == 0 {
		t.Fatal("no documented verbs found; this guard would assert nothing")
	}

	unanswered := map[string][]documentedVerb{}
	for _, mention := range documented {
		if !answerable[mention.verb] {
			unanswered[mention.verb] = append(unanswered[mention.verb], mention)
		}
	}
	if len(unanswered) == 0 {
		return
	}

	verbs := make([]string, 0, len(unanswered))
	for verb := range unanswered {
		verbs = append(verbs, verb)
	}
	sort.Strings(verbs)

	var report strings.Builder
	report.WriteString("governance documents name `cadre` verbs this CLI does not answer:\n")
	for _, verb := range verbs {
		report.WriteString("\n  " + verb + "\n")
		for _, mention := range unanswered[verb] {
			relative, relErr := filepath.Rel(repo, mention.file)
			if relErr != nil {
				relative = mention.file
			}
			report.WriteString("    " + relative + ":" +
				strconv.Itoa(mention.line) + "\n")
		}
	}
	report.WriteString("\nEither build it, or add it to retiredVerbs / pythonEraVerbs " +
		"in internal/cli/knowledge.go so a reader following the document is told " +
		"what happened. Deleting the mention is also a fix, but only if the " +
		"capability really is gone rather than merely undocumented.")
	t.Error(report.String())
}

// TestTheLiveVerbListMatchesWhatTheDispatcherAnswers keeps the declared list
// honest. A verb in liveKnowledgeVerbs that the switch does not handle would
// be reported as answerable while falling through to "unknown subcommand" --
// the drift check above would then pass on a lie.
func TestTheLiveVerbListMatchesWhatTheDispatcherAnswers(t *testing.T) {
	for _, verb := range liveKnowledgeVerbs {
		// A *resolvable* config, deliberately. The first version of this test
		// passed a nonexistent one, which fails in LoadConfig before the
		// dispatch switch is ever reached -- so a phantom verb added to the
		// list survived the mutation that should have killed it. The test was
		// asserting that config resolution fails, which was never in doubt.
		//
		// With a real config each live verb reaches dispatch and refuses on
		// its own terms without creating anything: `init` declines a store
		// that does not exist, `search` wants a query, `config` prints.
		cfgPath := writeStoreConfig(t, filepath.Join(t.TempDir(), "store.db"), nil)
		stderr := captureStderr(t, func() {
			_ = KnowledgeCmd([]string{"--config", cfgPath, verb})
		})
		if strings.Contains(stderr, "unknown subcommand") {
			t.Errorf("%q is declared live but the dispatcher does not answer it", verb)
		}
	}
}

// TestTheGuardReachesTheRepositoryRoot pins the scan surface that CP-4 found
// missing.
//
// The capability-parity ultragoal corrected two documents at the repository
// root -- RELEASE_NOTES_PHASE4.md and PHASE4_ROADMAP.md -- that had announced
// retention and deletion as complete and production-ready two hours before
// the commit removing them. The corrections were sound. They also had no
// guard behind them: this test's own scan roots were roster/ and
// .agents/skills/, so a phantom verb injected into either root document
// passed while the same injection into roster/ failed.
//
// The criterion those corrections served claims to be enforced rather than
// restored. It was not, for those two files, and nothing said so -- the
// per-criterion checks passed because they read the files rather than the
// mechanism. This test asserts the mechanism, so a future narrowing of the
// scan roots fails here rather than being discovered by the next audit.
func TestTheGuardReachesTheRepositoryRoot(t *testing.T) {
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, "roster")); err != nil {
		t.Skipf("no roster/ beside this package: %v", err)
	}

	mentions := scanRepoRootMarkdown(t, repo)
	if len(mentions) == 0 {
		t.Fatal("the repository root yielded no verb mentions; the root scan is " +
			"wired up but reaching nothing, which asserts as little as not scanning it")
	}

	// Every mention must come from a root-level file, never a nested one:
	// the root scan is deliberately non-recursive, because the root also
	// holds the generated and vendored trees.
	for _, mention := range mentions {
		if filepath.Dir(mention.file) != repo {
			t.Errorf("root scan recursed into %s; it must stay at the top level", mention.file)
		}
	}

	// And the history exclusion must still hold, or the guard would force a
	// dated decision log to be rewritten to satisfy it.
	lines := []string{
		"Run `cadre knowledge search` for this.",
		"## 8. Decision Log",
		"- Removed the undocumented `cadre zzzhistoric` command.",
	}
	kept := scanMarkdownLines("x.md", linesBeforeDecisionLog(lines))
	if len(kept) != 1 || kept[0].verb != "knowledge search" {
		t.Fatalf("expected only the pre-log mention to survive, got %+v", kept)
	}
}
