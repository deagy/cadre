package kernel

import (
	"os"
	"path/filepath"
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

func TestTheKernelVersionIsInsideEveryProviderCompatibilityWindow(t *testing.T) {
	// Version was guarded against the Python kernel's literal until that
	// kernel was deleted. The reason it needed guarding did not go with it:
	// providers declare a kernel_compatibility range and are refused outside
	// it, so a wrong value here either rejects a provider that should load or
	// accepts one written against different gate semantics.
	//
	// With one implementation left, the thing it must agree with is the
	// provider bundles this repository ships. This also replaces the Python
	// repository-health check that ran `cadre sdlc --version` and compared it
	// to the same window -- now a constant comparison, with no subprocess.
	root := repositoryRoot(t)
	manifests, err := filepath.Glob(filepath.Join(root, "provider*", "**", "provider.json"))
	if err != nil {
		t.Fatal(err)
	}
	direct, err := filepath.Glob(filepath.Join(root, "provider*", "provider.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifests = append(manifests, direct...)
	if len(manifests) == 0 {
		t.Fatal("no provider manifests found; this guard is not checking anything")
	}

	current, err := semverTuple(Version)
	if err != nil {
		t.Fatalf("the kernel version %q is not a semantic version: %v", Version, err)
	}
	for _, path := range manifests {
		manifest, err := loadJSONObject(path)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		window, ok := manifest["kernel_compatibility"].(map[string]any)
		if !ok {
			continue // a manifest that declares no window constrains nothing
		}
		minimum, err := semverTuple(toStringOrEmpty(window["minimum"]))
		if err != nil {
			t.Errorf("%s: minimum %v", path, err)
			continue
		}
		maximum, err := semverTuple(toStringOrEmpty(window["maximum_exclusive"]))
		if err != nil {
			t.Errorf("%s: maximum_exclusive %v", path, err)
			continue
		}
		// The same comparison LoadProvider makes, so this fails exactly when a
		// real load would.
		if semverLessThan(current, minimum) || !semverLessThan(current, maximum) {
			t.Errorf("%s declares [%v, %v) and this kernel is %s -- it would refuse "+
				"its own provider bundle", path, window["minimum"],
				window["maximum_exclusive"], Version)
		}
	}
}
