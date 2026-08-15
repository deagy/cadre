// Package kernel is the Go port of the G1–G10 lifecycle kernel's CLI.
//
// It exists as its own binary (cmd/agentic-sdlc), not as a library the
// selector links against, and that separation is the point rather than an
// accident of layout.
//
// kernel/ owns lifecycle gate schemas, run-record validation, and
// gate-authority semantics -- permanently. roster/ asks; the kernel answers.
// While the kernel was Python and roster/ was Python, two processes enforced
// that by construction. In one Go module nothing stops an in-process import
// that dissolves the boundary, and the change would look small and reasonable
// in review: delete a subprocess, call a function. kernel_boundary_test.go is
// what replaces the guarantee the process split used to give for free.
package kernel

import (
	"embed"
	"fmt"
	"sort"
	"strings"
)

// contractFiles are the bundled lifecycle contracts.
//
// Embedded rather than read from disk so the binary is self-contained: a
// kernel distributed as a binary cannot assume a checkout is present, and a
// contract read from a path the caller influences is a contract the caller
// can substitute.
//
// The copy under internal/kernel/contracts/ is guarded against
// kernel/contracts/ by TestEmbeddedContractsMatchTheSourceOfTruth -- the same
// arrangement the packaged plugin uses, where duplication is deliberate and a
// test is what keeps it honest.
//
//go:embed contracts/*.json
var contractFiles embed.FS

// ContractNames are the contracts `show-contract` will print, in the order
// the Python kernel's argparse `choices` list declares them.
//
// A fixed list, not a directory listing: the set of contracts a caller may
// ask for is part of the CLI's contract, and deriving it from whatever
// happens to be on disk would let a stray file become a valid argument.
var ContractNames = []string{
	"artifact.schema",
	"agent-catalog.schema",
	"dispatch-bindings.schema",
	"extension.schema",
	"lifecycle-gates",
	"mutation-gates",
	"profile.schema",
	"provider.schema",
	"run-record.schema",
	"selection.schema",
}

// ShowContract returns the named contract exactly as `show-contract` prints
// it: the file's content with trailing whitespace removed, plus one newline.
//
// The exact bytes matter. `cadre select` parses this output, so a difference
// as small as a trailing newline is a difference in what the selector reads.
func ShowContract(name string) (string, error) {
	if !isKnownContract(name) {
		return "", fmt.Errorf(
			"argument name: invalid choice: %q (choose from %s)",
			name, strings.Join(quoteAll(ContractNames), ", "))
	}
	data, err := contractFiles.ReadFile("contracts/" + name + ".json")
	if err != nil {
		return "", fmt.Errorf("cannot read contract %q: %w", name, err)
	}
	return strings.TrimRight(string(data), " \t\r\n") + "\n", nil
}

func isKnownContract(name string) bool {
	for _, known := range ContractNames {
		if known == name {
			return true
		}
	}
	return false
}

func quoteAll(values []string) []string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, "'"+value+"'")
	}
	return quoted
}

// EmbeddedContractNames lists what is actually embedded, for the drift guard.
func EmbeddedContractNames() ([]string, error) {
	entries, err := contractFiles.ReadDir("contracts")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}

// EmbeddedContract returns one embedded contract's raw bytes.
func EmbeddedContract(filename string) ([]byte, error) {
	return contractFiles.ReadFile("contracts/" + filename)
}
