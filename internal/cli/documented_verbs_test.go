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

// knowledgeVerbPattern matches the verb in a documented invocation, in prose
// or in a fenced usage block. Backticks are optional because the README's
// usage block has none.
var knowledgeVerbPattern = regexp.MustCompile(`cadre knowledge ([a-z][a-z0-9-]*)`)

// scanDocumentedKnowledgeVerbs walks the roster for verbs the documents name.
func scanDocumentedKnowledgeVerbs(t *testing.T, root string) []documentedVerb {
	t.Helper()
	var found []documentedVerb

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for index, text := range strings.Split(string(content), "\n") {
			for _, match := range knowledgeVerbPattern.FindAllStringSubmatch(text, -1) {
				found = append(found, documentedVerb{
					verb: match[1],
					file: path,
					line: index + 1,
				})
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

	answerable := AnswerableKnowledgeVerbs()
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
	report.WriteString("governance documents name verbs this CLI answers with " +
		"\"unknown subcommand\":\n")
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
