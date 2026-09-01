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
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		inFence := false
		for index, text := range strings.Split(string(content), "\n") {
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
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return found
}

// TestEveryDocumentedKnowledgeVerbIsAnswerable.
//
// "Answerable" is a low bar on purpose: running, naming a replacement, or
// admitting it was never built all pass. What fails is silence.
func TestEveryDocumentedKnowledgeVerbIsAnswerable(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "roster"))
	if err != nil {
		t.Fatal(err)
	}
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

	documented := scanDocumentedKnowledgeVerbs(t, root)
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
			relative, relErr := filepath.Rel(filepath.Dir(root), mention.file)
			if relErr != nil {
				relative = mention.file
			}
			report.WriteString("    " + relative + ":" +
				strconv.Itoa(mention.line) + "\n")
		}
	}
	report.WriteString("\nEither build it, or add it to retiredVerbs / neverShippedVerbs " +
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
