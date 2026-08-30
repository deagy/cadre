package knowledge

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The retrieval contract, driven from testdata rather than restated here.
//
// This exists because the contract lives inside Store.Search and nowhere else.
// When the retrieval engine moves to recall, the refusals do not come with it:
// recall's Search takes filters a caller may omit and spans all namespaces by
// default, which is correct for a retrieval library and wrong for a governed
// retrieval interface. Whatever replaces this store has to refuse the same
// things, and this file is what it will be measured against.
//
// Captured while both the behaviour and its implementation are present, for
// the same reason the selector/kernel fingerprint agreement was frozen before
// the kernel moved: afterwards it could only be reconstructed from one side's
// memory of what it used to do.
const failClosedContractPath = "testdata/fail-closed-contract.json"

type contractCase struct {
	Name           string   `json:"name"`
	Why            string   `json:"why"`
	Query          string   `json:"query"`
	Classification string   `json:"classification"`
	Provider       bool     `json:"provider"`
	AllSources     bool     `json:"all_sources"`
	SourceFilters  []string `json:"source_filters"`
	ExpectRefusal  string   `json:"expect_refusal"`
}

func loadContract(t *testing.T) []contractCase {
	t.Helper()
	raw, err := os.ReadFile(failClosedContractPath)
	if err != nil {
		t.Fatalf("read %s: %v", failClosedContractPath, err)
	}
	var file struct {
		Cases []contractCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("parse %s: %v", failClosedContractPath, err)
	}
	if len(file.Cases) == 0 {
		t.Fatalf("%s holds no cases; this guard would assert nothing", failClosedContractPath)
	}
	return file.Cases
}

// contractProvider is a stand-in. Every case in the contract is refused before
// any embedding is computed, so this is never called -- and if a refusal ever
// stops happening first, Embed failing the test says so directly.
type contractProvider struct{ t *testing.T }

func (p contractProvider) Embed(texts []string) ([][]float64, error) {
	p.t.Error("a contract case reached the embedding provider; a refusal that " +
		"should happen before any work now happens after it")
	return nil, nil
}
func (p contractProvider) Name() string    { return "contract" }
func (p contractProvider) Model() string   { return "contract" }
func (p contractProvider) Dimensions() int { return 1 }

// TestTheStoreRefusesEveryContractCase drives the live store against the
// recorded contract. It needs no database: every refusal happens before the
// store is touched, which is itself part of the contract -- a governed
// interface that only refuses after opening a connection has already leaked
// the fact that the caller asked.
func TestTheStoreRefusesEveryContractCase(t *testing.T) {
	store := &Store{}
	for _, testCase := range loadContract(t) {
		t.Run(testCase.Name, func(t *testing.T) {
			opts := SearchOptions{
				Query:          testCase.Query,
				Classification: testCase.Classification,
				AllSources:     testCase.AllSources,
				SourceFilters:  testCase.SourceFilters,
			}
			if testCase.Provider {
				opts.EmbeddingProvider = contractProvider{t: t}
			}
			results, err := store.Search(opts)
			if err == nil {
				t.Fatalf("accepted. %s", testCase.Why)
			}
			if results != nil {
				t.Error("results returned alongside a refusal")
			}
			if !strings.Contains(err.Error(), testCase.ExpectRefusal) {
				t.Errorf("refused for the wrong reason.\n  got:  %v\n  want: contains %q\n  %s",
					err, testCase.ExpectRefusal, testCase.Why)
			}
		})
	}
}

// A contract that lost a case would still pass the test above. This is what
// stops the fixture being quietly narrowed to whatever the implementation
// happens to do.
func TestTheContractStillCoversEveryRefusal(t *testing.T) {
	seen := map[string]bool{}
	for _, testCase := range loadContract(t) {
		seen[testCase.ExpectRefusal] = true
	}
	for _, required := range []string{
		"query is required",
		"classification is required",
		"embedding provider is required",
		"source scope is required",
		"source scope is ambiguous",
		"must be non-empty",
	} {
		if !seen[required] {
			t.Errorf("the contract no longer covers %q; a refusal was dropped from the fixture", required)
		}
	}
}
