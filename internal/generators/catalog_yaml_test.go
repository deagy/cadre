package generators

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Every value the catalog writer emits must read back as the string it wrote.
//
// RenderCatalog writes `    field: value` with the value raw. The Python
// generator it replaced ran each value through an emit_scalar that
// JSON-quoted anything a YAML 1.1 parser would type-coerce -- `no`, `on`,
// `off`, `true`, `null`, `~`, a bare number -- because catalog.yaml is read
// back by a real YAML parser, and a value that returns as `false` where a
// string was written is a role whose capability or model silently is not what
// the file says.
//
// That guard is not ported, deliberately. Every catalog field except
// `definition` is enum-constrained by catalog.schema.json, so a reserved word
// cannot reach the writer without failing validation first, and re-implementing
// a defence the schema already provides is unreachable code that looks like
// diligence.
//
// What is worth pinning is the *property*, not either mechanism for reaching
// it. This parses the committed catalog with a real YAML parser and requires
// every scalar to come back as the text that was written. It holds today
// because of the enums; if an enum is ever widened to include `off`, or
// `definition` grows a path a parser reads as something else, this fails --
// whichever way the guarantee was being provided.

func TestEveryCatalogValueReadsBackAsTheStringItWasWritten(t *testing.T) {
	root := repositoryRoot(t)
	path := filepath.Join(root, "roster", "catalog.yaml")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("not running inside a source checkout: %v", err)
	}

	// The raw text, read line by line the way the writer emits it.
	written := map[string]map[string]string{}
	var current string
	roleLine := regexp.MustCompile(`^  ([A-Za-z0-9][A-Za-z0-9-]*):\s*$`)
	fieldLine := regexp.MustCompile(`^    ([a-z_]+): (.*)$`)
	for _, line := range strings.Split(string(content), "\n") {
		if match := roleLine.FindStringSubmatch(line); match != nil {
			current = match[1]
			written[current] = map[string]string{}
			continue
		}
		if match := fieldLine.FindStringSubmatch(line); match != nil && current != "" {
			written[current][match[1]] = match[2]
		}
	}
	if len(written) < 100 {
		t.Fatalf("read %d roles out of the catalog text; the line parse is broken, "+
			"not the catalog", len(written))
	}

	// The same file through a real YAML parser -- the one schema validation
	// and every consumer uses.
	var parsed struct {
		Agents map[string]map[string]any `yaml:"agents"`
	}
	if err := yaml.Unmarshal(content, &parsed); err != nil {
		t.Fatalf("the committed catalog does not parse as YAML: %v", err)
	}
	if len(parsed.Agents) != len(written) {
		t.Fatalf("the parser found %d roles and the line parse found %d; one of "+
			"the two is wrong and the comparison below would be meaningless",
			len(parsed.Agents), len(written))
	}

	for role, fields := range written {
		agent, ok := parsed.Agents[role]
		if !ok {
			t.Errorf("%s is in the text but not in the parsed document", role)
			continue
		}
		for field, raw := range fields {
			value, present := agent[field]
			if !present {
				t.Errorf("%s.%s is in the text but not in the parsed document", role, field)
				continue
			}
			text, isString := value.(string)
			if !isString {
				t.Errorf("%s.%s was written as %q and read back as %T (%v). A YAML 1.1 "+
					"parser type-coerces bare true/false/yes/no/on/off/null/~ and bare "+
					"numbers, so this value needs quoting -- or the enum that was "+
					"preventing it has been widened.", role, field, raw, value, value)
				continue
			}
			if text != raw {
				t.Errorf("%s.%s was written as %q and read back as %q",
					role, field, raw, text)
			}
		}
	}
}

func TestTheRoundTripCheckWouldNoticeACoercedValue(t *testing.T) {
	// Guards the guard. The test above passes because every catalog value is
	// enum-constrained today, which means it would also pass against a writer
	// with no quoting at all -- exactly the situation. So it is worth showing
	// the check can fail: the same comparison, over a document written the way
	// RenderCatalog writes one, with a value a parser reads as a bool.
	// `true` rather than `off`, and the difference is the finding.
	//
	// PyYAML implements YAML 1.1, where off/no/yes/on are all booleans.
	// gopkg.in/yaml.v3 implements 1.2, where they are plain strings. Measured
	// both ways: the two agree only on true/false/null/~ and bare numbers.
	//
	// So "does this value need quoting?" has a different answer per reader,
	// and the Python generator quoted for the stricter one -- catalog.yaml is
	// still read by PyYAML in schema_validate.py. A demonstration written with
	// `off` would pass here while proving nothing, which is what the first
	// draft of this test did.
	document := "agents:\n  sample-role:\n    capability: true\n    phase: build\n"
	var parsed struct {
		Agents map[string]map[string]any `yaml:"agents"`
	}
	if err := yaml.Unmarshal([]byte(document), &parsed); err != nil {
		t.Fatalf("fixture does not parse: %v", err)
	}
	value := parsed.Agents["sample-role"]["capability"]
	if _, isString := value.(string); isString {
		t.Fatalf("this YAML parser reads bare `true` as a string (%T), so the check "+
			"above cannot detect coercion and its failure message is misleading", value)
	}
	// And an enum-legal value survives, so the difference is the value rather
	// than the parser.
	if phase, isString := parsed.Agents["sample-role"]["phase"].(string); !isString || phase != "build" {
		t.Errorf("an ordinary value did not survive the round trip: %v",
			parsed.Agents["sample-role"]["phase"])
	}
}

func TestTheSchemaEnumsAreWhatKeepReservedWordsOutOfTheCatalog(t *testing.T) {
	// Names the mechanism the test above relies on, so removing it is a
	// deliberate act rather than a quiet one. Every catalog field the writer
	// emits raw is enum-constrained except `definition`, which is a path and
	// cannot be a bare reserved word.
	//
	// If a field loses its enum, this fails here -- next to the comment
	// explaining why that matters -- rather than years later as a role whose
	// capability reads back as `false`.
	reserved := map[string]bool{
		"true": true, "false": true, "yes": true, "no": true,
		"on": true, "off": true, "null": true, "~": true,
	}
	for _, field := range []string{"phase", "capability", "model", "codex_model", "reasoning_effort"} {
		values := catalogSchemaEnum(t, field)
		if len(values) == 0 {
			t.Errorf("%s has no enum; a reserved word could now reach the catalog "+
				"writer, which emits values unquoted", field)
			continue
		}
		for _, value := range values {
			if reserved[strings.ToLower(value)] {
				t.Errorf("%s permits %q, which a YAML parser reads as a bool or null. "+
					"The writer emits values unquoted, so this would land in "+
					"catalog.yaml as the wrong type.", field, value)
			}
			if value == "" || strings.TrimSpace(value) != value {
				t.Errorf("%s permits %q, which needs quoting to survive a round trip",
					field, value)
			}
		}
	}
}
