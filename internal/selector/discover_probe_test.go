package selector

import (
	"encoding/json"
	"os"
	"testing"
)

// Env-gated parity probe, matching the pattern the rest of this package uses.
// It is skipped in an ordinary `go test ./...` run and driven by
// roster/orchestration/test/probe_discover_parity.py, which builds the
// checkouts, asks Python the same questions, and diffs the two answers.
//
// The driver owns checkout construction because several of these values
// depend on the checkout's own path -- the local-<name>-<digest> source in
// particular -- so comparing across two independently-created temp trees
// would report differences that are artefacts of the harness.

type discoverProbeCase struct {
	Root string `json:"root"`
	Base string `json:"base"`
}

type discoverProbeAnswer struct {
	Root              string   `json:"root"`
	ChangedFileSource string   `json:"changed_file_source"`
	ChangedFiles      []string `json:"changed_files"`
	ChangedFilesError string   `json:"changed_files_error"`
	OriginSlug        string   `json:"origin_slug"`
	HasOriginSlug     bool     `json:"has_origin_slug"`
	ProjectSource     string   `json:"project_source"`
	KnowledgeSources  []string `json:"knowledge_sources"`
	ProjectLocalStore bool     `json:"project_local_store"`
}

func TestDiscoverParityProbe(t *testing.T) {
	inputPath := os.Getenv("CADRE_DISCOVER_PROBE_IN")
	outputPath := os.Getenv("CADRE_DISCOVER_PROBE_OUT")
	if inputPath == "" || outputPath == "" {
		t.Skip("set CADRE_DISCOVER_PROBE_IN and CADRE_DISCOVER_PROBE_OUT to run the parity probe")
	}

	raw, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	var cases []discoverProbeCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}

	answers := make([]discoverProbeAnswer, 0, len(cases))
	for _, probe := range cases {
		answer := discoverProbeAnswer{Root: probe.Root}

		changed, err := DiscoverChangedFiles(probe.Base, probe.Root)
		if err != nil {
			answer.ChangedFilesError = err.Error()
		} else {
			answer.ChangedFileSource = changed.Source
			answer.ChangedFiles = changed.Files
		}
		if answer.ChangedFiles == nil {
			answer.ChangedFiles = []string{}
		}

		answer.OriginSlug, answer.HasOriginSlug = OriginSlug(probe.Root)
		answer.ProjectSource = ResolveProjectKnowledgeSource(probe.Root)
		answer.KnowledgeSources = ResolveKnowledgeSources(probe.Root)
		answer.ProjectLocalStore = HasProjectLocalKnowledgeStore(probe.Root)

		answers = append(answers, answer)
	}

	encoded, err := json.MarshalIndent(answers, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outputPath, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d answers to %s", len(answers), outputPath)
}

type explicitProbeCase struct {
	Files   []string `json:"files"`
	Sources []string `json:"sources"`
}

type explicitProbeAnswer struct {
	Files        []string `json:"files"`
	Sources      []string `json:"sources"`
	SourcesError string   `json:"sources_error"`
}

func TestExplicitInputParityProbe(t *testing.T) {
	inputPath := os.Getenv("CADRE_EXPLICIT_PROBE_IN")
	outputPath := os.Getenv("CADRE_EXPLICIT_PROBE_OUT")
	if inputPath == "" || outputPath == "" {
		t.Skip("set CADRE_EXPLICIT_PROBE_IN and CADRE_EXPLICIT_PROBE_OUT to run the parity probe")
	}

	raw, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	var cases []explicitProbeCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}

	answers := make([]explicitProbeAnswer, 0, len(cases))
	for _, probe := range cases {
		answer := explicitProbeAnswer{Files: ExplicitFiles(probe.Files)}
		if answer.Files == nil {
			answer.Files = []string{}
		}
		sources, err := NormalizeExplicitSources(probe.Sources)
		if err != nil {
			answer.SourcesError = err.Error()
		}
		if sources == nil {
			sources = []string{}
		}
		answer.Sources = sources
		answers = append(answers, answer)
	}

	encoded, err := json.MarshalIndent(answers, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outputPath, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d answers to %s", len(answers), outputPath)
}
