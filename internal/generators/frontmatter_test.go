package generators

import (
	"strings"
	"testing"
)

// Finding where a role's frontmatter ends.
//
// Every AGENT.md under roster/ may carry a `---`-delimited metadata block, and
// the packaging copy strips it before writing the suite copy. Getting the
// boundary wrong is not a parse error -- it silently moves text between the
// metadata and the body, so either the brief loses its opening paragraphs or
// the packaged file carries frontmatter a reader was never meant to see.
//
// frontmatter.go had no test of its own; what covered it was the packaging
// end-to-end case, which only ever sees well-formed files.

func TestFrontmatterIsRecognisedOnlyAtByteZero(t *testing.T) {
	// The delimiter is a prefix rule, not a search. A file with `---`
	// somewhere in it is an ordinary document; a file *starting* with it is
	// declaring metadata. Anything looser makes a horizontal rule in prose
	// into a metadata block.
	for _, migrated := range []string{
		"---\nid: a\n---\n\nbody\n",
		"---\r\nid: a\r\n---\r\n\r\nbody\r\n",
	} {
		if !IsMigrated(migrated) {
			t.Errorf("a file opening with a delimiter was not recognised: %q",
				first80(migrated))
		}
	}
	for _, unmigrated := range []string{
		"\n---\nid: a\n---\n",             // a blank line first
		" ---\nid: a\n---\n",              // indented
		"\ufeff---\nid: a\n---\n",         // a byte-order mark
		"# Heading\n\n---\n\nbody\n",      // a horizontal rule in prose
		"----\nid: a\n----\n",             // four dashes
		"--- \nid: a\n---\n",              // trailing space on the opener
		"",                                // empty
		"body with no frontmatter at all", // ordinary text
	} {
		if IsMigrated(unmigrated) {
			t.Errorf("a file that does not open with a delimiter was treated as "+
				"migrated: %q", first80(unmigrated))
		}
	}
}

func TestAnEmbeddedTripleDashDoesNotCloseTheBlockEarly(t *testing.T) {
	// The reason the scan matches whole lines rather than searching for the
	// substring. A field value mentioning `---` appears before the real
	// closing delimiter, so a raw search finds the wrong one and the split
	// lands mid-metadata: the packaged file would keep half its frontmatter
	// and lose the start of its brief.
	text := "---\n" +
		"id: sample-role\n" +
		"knowledge_focus: prior --- separators in generated output\n" +
		"---\n" +
		"\n" +
		"# Sample\n\nThe body.\n"

	body, err := StripFrontmatter(text)
	if err != nil {
		t.Fatalf("StripFrontmatter: %v", err)
	}
	if strings.Contains(body, "knowledge_focus") {
		t.Errorf("the split landed inside the frontmatter; the body still carries "+
			"metadata:\n%s", body)
	}
	if !strings.HasPrefix(body, "\n# Sample") {
		t.Errorf("the body did not start where the frontmatter ended:\n%q", body)
	}

	end, ok, err := FrontmatterClosingDelimiterEnd(text)
	if err != nil || !ok {
		t.Fatalf("FrontmatterClosingDelimiterEnd: ok=%v err=%v", ok, err)
	}
	// The offset lands after the *real* closing line, not the one inside the
	// value: everything before it is metadata, and it ends with "---".
	if !strings.HasSuffix(text[:end], "---") {
		t.Errorf("the offset does not end at a delimiter: %q", text[:end])
	}
	if strings.Count(text[:end], "\n---") != 1 {
		t.Errorf("the offset spans more than one delimiter line: %q", text[:end])
	}
}

func TestStrippingLeavesTheBodyByteIdentical(t *testing.T) {
	// The packaged copy is compared byte-for-byte against the committed
	// distribution, so anything this normalises -- a trailing newline, an
	// indent, a run of blank lines -- becomes a diff in a file nobody edited.
	for _, probe := range []struct{ name, body string }{
		{"trailing blank lines", "# Role\n\nText.\n\n\n"},
		{"no trailing newline", "# Role\n\nText."},
		{"leading blank line", "\n# Role\n"},
		{"indented content", "# Role\n\n    indented\n\ttabbed\n"},
		{"an inner --- rule", "# Role\n\n---\n\nAfter a horizontal rule.\n"},
		{"trailing spaces", "# Role  \n\nText.  \n"},
	} {
		t.Run(probe.name, func(t *testing.T) {
			stripped, err := StripFrontmatter("---\nid: a\n---\n" + probe.body)
			if err != nil {
				t.Fatalf("StripFrontmatter: %v", err)
			}
			if stripped != probe.body {
				t.Errorf("the body changed.\nwant: %q\ngot:  %q", probe.body, stripped)
			}
		})
	}
}

func TestStrippingIsANoOpOnAFileWithoutFrontmatter(t *testing.T) {
	// Not every AGENT.md carries metadata, and one that does not must come
	// through untouched rather than losing its first paragraph to a delimiter
	// search that found something.
	for _, text := range []string{
		"# Role\n\nNo frontmatter here.\n",
		"# Role\n\n---\n\nA rule, not a delimiter.\n",
		"",
	} {
		stripped, err := StripFrontmatter(text)
		if err != nil {
			t.Fatalf("StripFrontmatter(%q): %v", first80(text), err)
		}
		if stripped != text {
			t.Errorf("an unmigrated file was altered.\nwant: %q\ngot:  %q", text, stripped)
		}
	}
}

func TestAnUnclosedFrontmatterBlockIsAnError(t *testing.T) {
	// A file opening with a delimiter and never closing it is truncated or
	// mid-edit. Treating the whole file as metadata would package an empty
	// brief; treating it as body would package the metadata. Neither is a
	// guess worth making.
	for _, text := range []string{
		"---\nid: a\n",
		"---\nid: a\nno closing delimiter here\n",
		"---\n",
		"---\nid: a\n----\n",  // four dashes is not the delimiter
		"---\nid: a\n --- \n", // indented is not the delimiter
	} {
		if _, err := StripFrontmatter(text); err == nil {
			t.Errorf("an unclosed block was stripped without complaint: %q", first80(text))
		}
		if _, ok, err := FrontmatterClosingDelimiterEnd(text); err == nil && ok {
			t.Errorf("an unclosed block reported a closing delimiter: %q", first80(text))
		}
	}
}

func TestCarriageReturnsAreCarriedThrough(t *testing.T) {
	// A file written on Windows keeps its line endings. Normalising them here
	// would rewrite every line of the packaged copy, and the byte-for-byte
	// check against the committed distribution would fail on a file whose
	// content nobody changed.
	text := "---\r\nid: a\r\n---\r\n\r\n# Role\r\n\r\nText.\r\n"
	stripped, err := StripFrontmatter(text)
	if err != nil {
		t.Fatalf("StripFrontmatter: %v", err)
	}
	want := "\r\n# Role\r\n\r\nText.\r\n"
	if stripped != want {
		t.Errorf("CRLF body changed.\nwant: %q\ngot:  %q", want, stripped)
	}
	if strings.Contains(strings.ReplaceAll(stripped, "\r\n", ""), "\n") {
		t.Errorf("a bare newline appeared in a CRLF document: %q", stripped)
	}
}

func first80(text string) string {
	if len(text) <= 80 {
		return text
	}
	return text[:80] + "..."
}
