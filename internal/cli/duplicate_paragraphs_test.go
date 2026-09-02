package cli

// A stale paragraph left standing beside its own corrected rewrite is the
// defect this file exists to catch.
//
// It happened four times in one project, in four different shapes: a
// verbatim duplicate 19 lines above its correction, a contradicting claim 21
// lines above the section that corrected it, an untouched paragraph directly
// below a banner saying the opposite, and a stale bullet immediately above
// the corrected one. Three separate verification rounds read these files
// looking for exactly this and found none of them. A thirty-line similarity
// check found all four in one pass.
//
// That gap is the argument for the test. Careful reading is good at judging
// whether a paragraph is true and bad at noticing it has a twin, because the
// twin reads correctly on its own -- the defect is not in either paragraph,
// it is in their coexistence. That is a mechanical property, so a machine
// should check it.
//
// Scoped to the governance documents rather than the whole tree: shared
// boilerplate is legitimately repeated across role files, and a guard that
// fires on intentional repetition would be turned off.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// duplicateParagraphThreshold is the bigram-overlap ratio above which two
// paragraphs in one document are reported.
//
// Calibrated against the real corpus: the highest legitimate pair sits well
// below this, and every historical defect sat well above it. It is a ratio
// rather than an exact match because the failures were near-duplicates -- a
// paragraph edited in one place and left alone in another -- so an
// equality check would have caught none of them.
const duplicateParagraphThreshold = 0.6

// governanceDocuments are the files whose internal consistency this checks.
func governanceDocuments(t *testing.T, repo string) []string {
	t.Helper()
	var paths []string
	for _, pattern := range []string{
		"roster/knowledge-store/*.md",
		"roster/workflows/*.md",
		"roster/context-store/*.md",
	} {
		matches, err := filepath.Glob(filepath.Join(repo, pattern))
		if err != nil {
			t.Fatalf("globbing %s: %v", pattern, err)
		}
		paths = append(paths, matches...)
	}
	for _, named := range []string{
		"roster/shared/knowledge-use-policy.md",
		"roster/operations/retention-and-deletion-executor/AGENT.md",
		".agents/skills/run-agent-orchestration/references/dispatch-contract.md",
	} {
		full := filepath.Join(repo, named)
		if _, err := os.Stat(full); err == nil {
			paths = append(paths, full)
		}
	}
	return paths
}

// paragraphBigrams reduces a paragraph to its set of adjacent word pairs.
//
// Bigrams rather than single words because word-set overlap is far too
// generous on prose from one document: two paragraphs about the same
// subsystem share most of their vocabulary while saying different things.
// Adjacent pairs capture phrasing, which is what a copied paragraph
// preserves and an independently written one does not.
func paragraphBigrams(text string) map[string]bool {
	fields := strings.Fields(strings.ToLower(text))
	bigrams := map[string]bool{}
	for index := 0; index+1 < len(fields); index++ {
		bigrams[fields[index]+" "+fields[index+1]] = true
	}
	return bigrams
}

// bigramOverlap is how much of the smaller paragraph appears in the larger.
//
// Containment, not Jaccard. Jaccard divides by the union, so a short stale
// paragraph beside a longer corrected one scores low purely because the
// correction added text -- which is the usual shape of the defect, since a
// correction generally explains more than the line it replaces. Measured
// that way the real AGENT.md defect scored 27% and went unreported. Asking
// instead how much of the shorter one survives inside the longer gives 75%,
// and that is the question worth asking: a paragraph almost wholly
// contained in another is a paragraph that was superseded and left behind.
func bigramOverlap(left, right map[string]bool) float64 {
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	shared := 0
	for bigram := range left {
		if right[bigram] {
			shared++
		}
	}
	smaller := len(left)
	if len(right) < smaller {
		smaller = len(right)
	}
	return float64(shared) / float64(smaller)
}

type documentParagraph struct {
	line int
	text string
}

// documentParagraphs splits a document into prose units worth comparing.
//
// Each list item is its own unit, not merged with the ones around it. That
// is not a detail: one of the four historical defects was a stale bullet
// sitting directly above its corrected replacement, with no blank line
// between them. A splitter that breaks only on blank lines merges the pair
// into a single unit and compares it against nothing, which is exactly what
// the first version of this file did -- it passed on the real defect.
//
// Fenced blocks are skipped: two shell examples differing by one flag are
// similar by construction and are not the defect. Units below the length
// floor are skipped for the same reason -- a repeated heading or a two-word
// list item carries no signal.
func documentParagraphs(content string) []documentParagraph {
	var paragraphs []documentParagraph
	var current []string
	currentLine := 1
	inFence := false
	flush := func(atLine int) {
		joined := strings.TrimSpace(strings.Join(current, " "))
		if len(joined) > 120 {
			paragraphs = append(paragraphs, documentParagraph{line: currentLine, text: joined})
		}
		current = nil
		currentLine = atLine
	}
	for index, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			flush(index + 2)
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if trimmed == "" {
			flush(index + 2)
			continue
		}
		if isListItem(trimmed) {
			flush(index + 1)
		}
		if len(current) == 0 {
			currentLine = index + 1
		}
		current = append(current, trimmed)
	}
	flush(0)
	return paragraphs
}

func TestNoGovernanceDocumentRepeatsItself(t *testing.T) {
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	documents := governanceDocuments(t, repo)
	if len(documents) == 0 {
		t.Skip("no governance documents beside this package")
	}

	for _, path := range documents {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		paragraphs := documentParagraphs(string(content))
		bigrams := make([]map[string]bool, len(paragraphs))
		for index, paragraph := range paragraphs {
			bigrams[index] = paragraphBigrams(paragraph.text)
		}
		relative, relErr := filepath.Rel(repo, path)
		if relErr != nil {
			relative = path
		}
		for a := range paragraphs {
			for b := a + 1; b < len(paragraphs); b++ {
				ratio := bigramOverlap(bigrams[a], bigrams[b])
				if ratio <= duplicateParagraphThreshold {
					continue
				}
				t.Errorf(
					"%s: the paragraphs at lines %d and %d are %.0f%% the same phrasing.\n"+
						"  A paragraph edited in one place and left standing in another reads\n"+
						"  correctly on its own, which is why review misses it. Delete the stale\n"+
						"  one, or reword them so they are not two answers to one question.\n"+
						"    line %d: %s...\n"+
						"    line %d: %s...",
					relative, paragraphs[a].line, paragraphs[b].line, ratio*100,
					paragraphs[a].line, truncateParagraph(paragraphs[a].text),
					paragraphs[b].line, truncateParagraph(paragraphs[b].text))
			}
		}
	}
}

// isListItem reports whether a line begins a bullet or numbered item.
func isListItem(trimmed string) bool {
	for _, marker := range []string{"- ", "* ", "+ "} {
		if strings.HasPrefix(trimmed, marker) {
			return true
		}
	}
	digits := 0
	for digits < len(trimmed) && trimmed[digits] >= '0' && trimmed[digits] <= '9' {
		digits++
	}
	return digits > 0 && digits+1 < len(trimmed) &&
		(trimmed[digits] == '.' || trimmed[digits] == ')') && trimmed[digits+1] == ' '
}

func truncateParagraph(text string) string {
	if len(text) <= 90 {
		return text
	}
	return text[:90]
}
