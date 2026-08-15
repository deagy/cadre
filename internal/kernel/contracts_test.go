package kernel

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestEmbeddedContractsMatchTheSourceOfTruth(t *testing.T) {
	// kernel/contracts/ is the source of truth; internal/kernel/contracts/ is
	// an embedded copy, because go:embed cannot reach outside its own package
	// directory and a kernel shipped as a binary must not need a checkout to
	// answer.
	//
	// Duplication with a guard, the same arrangement the packaged plugin uses.
	// Without this test the two drift silently, and the failure is invisible:
	// the Go kernel would answer confidently with a contract nobody edited.
	sourceDir := filepath.Join("..", "..", "kernel", "contracts")
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		t.Skipf("not running inside a source checkout: %v", err)
	}

	var sourceNames []string
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".json" {
			sourceNames = append(sourceNames, entry.Name())
		}
	}
	sort.Strings(sourceNames)

	embedded, err := EmbeddedContractNames()
	if err != nil {
		t.Fatalf("listing embedded contracts: %v", err)
	}
	if len(sourceNames) == 0 {
		t.Fatal("no contracts found on disk; this guard would be vacuous")
	}
	if len(sourceNames) != len(embedded) {
		t.Errorf("embedded %d contracts, source has %d:\n  embedded: %v\n  source:   %v",
			len(embedded), len(sourceNames), embedded, sourceNames)
	}

	for _, name := range sourceNames {
		want, err := os.ReadFile(filepath.Join(sourceDir, name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		got, err := EmbeddedContract(name)
		if err != nil {
			t.Errorf("%s is not embedded: %v", name, err)
			continue
		}
		if string(got) != string(want) {
			t.Errorf("%s differs between kernel/contracts/ and the embedded copy; "+
				"copy the source file over and rebuild", name)
		}
	}
}

func TestEveryOfferedContractNameResolves(t *testing.T) {
	// The offered list and the embedded set are declared separately, so one
	// can name something the other does not have. A caller asking for a
	// documented contract must not get an error about a missing file.
	for _, name := range ContractNames {
		contract, err := ShowContract(name)
		if err != nil {
			t.Errorf("%s is offered but does not resolve: %v", name, err)
			continue
		}
		if contract == "" {
			t.Errorf("%s resolved to nothing", name)
		}
	}
}

func TestTheOfferedNamesAndTheEmbeddedFilesAreTheSameSet(t *testing.T) {
	// Two lists are declared separately -- ContractNames, and whatever
	// contracts/*.json embeds -- and they must describe the same thing.
	//
	// An embedded file nobody offers is unreachable, which is the harmless
	// direction. An offered name with no file is a documented contract that
	// errors when asked for, which is not. Comparing the sets catches both,
	// and catches them when a contract is added, which is when someone is
	// most likely to update one list and not the other.
	embedded, err := EmbeddedContractNames()
	if err != nil {
		t.Fatalf("listing embedded contracts: %v", err)
	}
	embeddedSet := map[string]bool{}
	for _, filename := range embedded {
		embeddedSet[strings.TrimSuffix(filename, ".json")] = true
	}
	offeredSet := map[string]bool{}
	for _, name := range ContractNames {
		offeredSet[name] = true
	}

	for name := range offeredSet {
		if !embeddedSet[name] {
			t.Errorf("%q is offered by the CLI but no contract file is embedded for it", name)
		}
	}
	for name := range embeddedSet {
		if !offeredSet[name] {
			t.Errorf("%q.json is embedded but the CLI does not offer it; adding a contract "+
				"is a deliberate act in both places, not a consequence of dropping in a file", name)
		}
	}
	if len(offeredSet) == 0 {
		t.Fatal("no contracts offered; this guard asserted nothing")
	}
}

func TestPathShapedArgumentsAreRefused(t *testing.T) {
	// Refused -- though not only by the name check: embed.FS rejects ".." on
	// its own, and every embedded file is already an offered name, so the
	// explicit check is defence in depth rather than the thing doing the
	// work here. Stated plainly because a comment claiming otherwise would
	// misdescribe what this test can show.
	for _, attempt := range []string{
		"../pyproject", "../../etc/passwd", "contracts/lifecycle-gates",
		"lifecycle-gates/../../pyproject", "./lifecycle-gates", "",
	} {
		if _, err := ShowContract(attempt); err == nil {
			t.Errorf("%q was accepted as a contract name", attempt)
		}
	}
}

func TestTheOutputEndsWithExactlyOneNewline(t *testing.T) {
	// `cadre select` parses this. Trailing whitespace is not cosmetic here --
	// the Python kernel rstrips the file and prints, and matching that
	// exactly is the whole point of the differential gate.
	for _, name := range ContractNames {
		contract, err := ShowContract(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(contract) < 2 {
			t.Fatalf("%s produced almost nothing", name)
		}
		if contract[len(contract)-1] != '\n' {
			t.Errorf("%s does not end with a newline", name)
		}
		if contract[len(contract)-2] == '\n' {
			t.Errorf("%s ends with more than one newline", name)
		}
	}
}

func TestTheGoKernelVersionMatchesThePythonOne(t *testing.T) {
	// Version is a literal here and a literal in agentic_sdlc/__init__.py,
	// because a binary cannot read the Python source and reading it at
	// runtime would make the version depend on a checkout being present.
	//
	// It is not decoration: providers declare a kernel_compatibility range
	// and are refused outside it, so a wrong version here either rejects a
	// provider that should load or accepts one written against different gate
	// semantics.
	source, err := os.ReadFile(filepath.Join("..", "..", "kernel", "agentic_sdlc", "__init__.py"))
	if err != nil {
		t.Skipf("not running inside a source checkout: %v", err)
	}
	match := regexp.MustCompile(`(?m)^VERSION = "([^"]+)"`).FindSubmatch(source)
	if match == nil {
		t.Fatal("could not find VERSION in the Python kernel; this guard is not checking anything")
	}
	if got := string(match[1]); got != Version {
		t.Errorf("kernel version disagrees: Go has %q, Python has %q", Version, got)
	}
}
