package cli

// The staged-records half of `cadre knowledge`: propose, show-staged,
// import-staged, disposition-staged, ingest-accepted, delete-staged.
//
// This subsystem is where the authorship/approval separation invariant that
// AGENTS.md and CLAUDE.md declare a hard rule is actually enforced for
// knowledge ingestion. roster/knowledge-store/SECURITY.md names four checks;
// two of them live on this file's `propose` and `import-staged` paths, and the
// other two live in internal/knowledge (DispositionStagedRecord and the
// ingest-time refusal). Removing any of them turns "an agent that materially
// changes an artifact cannot approve that same artifact" back into prose that
// nothing checks.
//
// It lives in its own file rather than in knowledge.go so the staged-record
// verbs stay reviewable as one unit -- the separation checks, the verbs they
// gate, and the reasons they exist are meant to be read together.

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/deagy/cadre/cli/internal/knowledge"
	"github.com/deagy/cadre/cli/internal/retrieval"
)

// knowledgeStagedSubcommands are the verbs this file owns. The dispatcher
// consults this set before handing `cadre knowledge ...` to KnowledgeCmd.
var knowledgeStagedSubcommands = []string{
	"propose",
	"show-staged",
	"import-staged",
	"disposition-staged",
	"ingest-accepted",
	"delete-staged",
}

// IsKnowledgeStagedSubcommand reports whether name is a staged-record verb.
func IsKnowledgeStagedSubcommand(name string) bool {
	for _, candidate := range knowledgeStagedSubcommands {
		if candidate == name {
			return true
		}
	}
	return false
}

// KnowledgeStagedRoute reports whether `cadre knowledge <args...>` names a
// staged-record verb.
//
// It skips leading flags rather than assuming args[0] is the subcommand,
// because KnowledgeCmd accepts `-config <path>` before the subcommand and a
// naive args[0] test would silently route `knowledge -config X propose` to the
// wrong handler.
func KnowledgeStagedRoute(args []string) bool {
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if !strings.HasPrefix(argument, "-") {
			return IsKnowledgeStagedSubcommand(argument)
		}
		// `-config path` consumes the next argument; `-config=path` does not.
		name := strings.TrimLeft(argument, "-")
		if name == "config" {
			index++
		}
	}
	return false
}

// KnowledgeStagedCmd runs one staged-record subcommand. It mirrors
// KnowledgeCmd's `-config` handling so the two halves of `cadre knowledge`
// resolve the same store.
func KnowledgeStagedCmd(args []string) int {
	fs := flag.NewFlagSet("cadre knowledge", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `Usage: cadre knowledge <subcommand> [options]

Staged-record subcommands:
  propose              Stage one proposed-knowledge record for steward disposition
  show-staged          Show one staged record in full, with its disposition history
  import-staged        Import a directory of staged-record files, atomically
  disposition-staged   Record a steward's accept/reject/defer decision
  ingest-accepted      Make steward-accepted staged records retrievable
  delete-staged        Delete a staged record, leaving evidence behind

Options:
`)
		fs.PrintDefaults()
	}
	configFlag := fs.String("config", "", "Path to a knowledge store config file (optional)")
	if err := fs.Parse(args); err != nil {
		return parseExitCode(err)
	}
	if fs.NArg() < 1 {
		fs.Usage()
		return 2
	}
	subcommand := fs.Arg(0)
	subArgs := fs.Args()[1:]

	cfg, err := knowledgeStagedConfig(*configFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre knowledge %s: %v\n", subcommand, err)
		return 1
	}

	result, err := runKnowledgeStaged(cfg, subcommand, subArgs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot render result: %v\n", err)
		return 1
	}
	fmt.Println(string(encoded))
	return 0
}

// knowledgeStagedDatabasePath resolves the store to operate on, and refuses
// the shared one.
//
// `--config` is a path to a config *file*, resolved through the same three
// tiers as every other knowledge subcommand (knowledge.LoadConfig). It used to
// be read as a raw database path here, which was correct only for as long as
// KnowledgeCmd did the same; now that KnowledgeCmd resolves a config, a second
// meaning for the same flag on sibling subcommands would be its own defect.
//
// The tier is not incidental -- it is the enforcement point. Staged records
// are per project: a finding about this repository belongs in this
// repository's partition, not in a store shared with every other project on
// the machine. Python enforced this in cli.py's _enforce_staging_scope, gating
// all eight staged verbs, and it was the one piece of the staged-record
// contract that did not survive the Go port -- it needed a config-tier concept
// that only landed alongside this change, so neither half could carry it
// alone.
//
// This is a scope gate, not an approval gate. It refuses a *destination*, so
// it applies to ingest-accepted like any other verb without bearing on that
// command's deliberate absence of an approval flag (see staged_ingest.go).
//
// Enforced here rather than left to --source discipline because SECURITY.md is
// explicit that caller flags are not authentication: a convention that nothing
// checks is the failure mode the contract was written to avoid.
func knowledgeStagedConfig(configFlag string) (*knowledge.Config, error) {
	cfg, tier, err := knowledge.LoadConfig(configFlag)
	if err != nil {
		return nil, err
	}
	if tier == knowledge.TierGlobalFallback {
		return nil, fmt.Errorf(
			"staged knowledge records are per project, so they cannot be written to the "+
				"shared global knowledge store (%s). Create .agents/knowledge-store/config.json "+
				"in this project (an empty {} is enough to claim a project-local partition), "+
				"or pass --config pointing at the store this project owns", cfg.Database)
	}
	return cfg, nil
}

// openStagedStore opens the staged store, migrating records out of a legacy
// combined store the first time.
//
// Staged records used to live in the same database as the corpus. That
// database is recall's now, and cadre no longer opens it with its own schema
// -- so records staged before this change would be stranded in a file only an
// older cadre could read. Copied once, on first open, leaving the legacy file
// untouched: a migration that deletes its source cannot be re-run after
// someone notices it went wrong.
func openStagedStore(cfg *knowledge.Config) (*knowledge.Store, error) {
	stagedPath := knowledge.StagedDatabasePath(cfg)
	_, statErr := os.Stat(stagedPath)
	migrating := os.IsNotExist(statErr)

	if migrating {
		copied, err := knowledge.MigrateStagedRecords(cfg.Database, stagedPath)
		switch {
		case err == nil && copied > 0:
			fmt.Fprintf(os.Stderr,
				"cadre knowledge: moved %d staged row(s) from %s into %s. The originals are "+
					"left in place.\n", copied, cfg.Database, stagedPath)
		case err != nil && !errors.Is(err, knowledge.ErrNoLegacyStagedRecords):
			return nil, err
		}
	}
	return knowledge.OpenStaged(stagedPath)
}

// runKnowledgeStaged opens the staged store and dispatches.
//
// The schema is installed by OpenStaged now. It used to be installed here,
// because the staged tables were added over a retrieval engine's schema and
// that engine had no business knowing about them. There is no engine to keep
// out of it any more: this store holds staged records and nothing else.
func runKnowledgeStaged(cfg *knowledge.Config, subcommand string, args []string) (any, error) {
	store, err := openStagedStore(cfg)
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()

	switch subcommand {
	case "propose":
		return knowledgeStagedPropose(store, args, os.Stdin)
	case "show-staged":
		return knowledgeStagedShow(store, args)
	case "import-staged":
		return knowledgeStagedImport(store, args)
	case "disposition-staged":
		return knowledgeStagedDisposition(store, args)
	case "ingest-accepted":
		return knowledgeStagedIngestAccepted(store, cfg, args)
	case "delete-staged":
		return knowledgeStagedDelete(store, args)
	default:
		return nil, fmt.Errorf("unknown staged-record subcommand: %s", subcommand)
	}
}

func knowledgeStagedReadSource(source string, stdin io.Reader) (string, error) {
	if source == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("cannot read from stdin: %w", err)
		}
		return string(data), nil
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", source, err)
	}
	return string(data), nil
}

// ---------------------------------------------------------------------------
// propose
// ---------------------------------------------------------------------------

// refuseAPreDispositionedRecord refuses a `propose` input that arrives already
// decided.
//
// SEPARATION CHECK 1 of 4 (roster/knowledge-store/SECURITY.md), and the
// strongest of them: `propose` is the one verb non-steward agents may run
// against this store, and the whole reason that is safe is that a proposal
// cannot approve itself. The contract validator checks only that `status` and
// `disposition.action` *agree*, and type-checks `decided_by`; the rule that a
// record's stager cannot also be its decider lives on the `disposition-staged`
// path -- the verb an agent is not supposed to use. So a record handed to
// `propose --input` carrying `status: accepted` and a hand-written
// `disposition: {decided_by: knowledge-store-steward}` would be staged as
// written, and `ingest-accepted` would then make it retrievable: a proposing
// agent could author its own approval and reach the corpus without a steward
// touching the record.
//
// This check is **name-independent** -- it rejects the *shape* of a decided
// record, so no choice of identifier evades it. That is what makes it stronger
// than the two checks that compare caller-asserted identity strings.
//
// The fix is scoped to this verb rather than to the contract, deliberately. A
// dispositioned record is perfectly legitimate elsewhere: `import-staged`
// re-imports an existing corpus and `disposition-staged` produces them. What
// must not happen is a disposition *entering* through the proposal door.
//
// `--from-finding` cannot trip this: BuildStagedRecordFromFinding generates
// `status` as "proposed" and has no disposition input at all.
func refuseAPreDispositionedRecord(frontmatter map[string]any) error {
	status, present := frontmatter["status"]
	if present && status != "proposed" {
		return fmt.Errorf(
			"propose refuses a record whose status is %#v: a proposal is staged as 'proposed' and is "+
				"dispositioned only by a steward, through `disposition-staged`. Staging a decided record "+
				"here would let whoever wrote it record its approval. Use `import-staged` to load "+
				"already-dispositioned records", status)
	}
	if _, present := frontmatter["disposition"]; present {
		return errors.New(
			"propose refuses a record carrying a `disposition` block: the decision is the steward's to " +
				"record through `disposition-staged`, which enforces that a record's stager cannot also be " +
				"its decider. Remove the block and propose the record as 'proposed'")
	}
	return nil
}

// knowledgeStagedRenderResult validates and formats a not-yet-staged record for
// --render-only. It runs the real validator, not a preview-only
// approximation, so a render-only failure names the same problem `propose`
// would have refused on.
func knowledgeStagedRenderResult(frontmatter map[string]any, body string) (any, error) {
	if findings := knowledge.ValidateStagedRecord(frontmatter, body); len(findings) > 0 {
		return nil, fmt.Errorf("record does not satisfy the contract: %s", strings.Join(findings, "; "))
	}
	text, err := knowledge.SerializeStagedRecord(frontmatter, body)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"status":         "rendered",
		"id":             frontmatter["id"],
		"content_digest": frontmatter["content_digest"],
		"text":           text,
		"note": "Not staged: --render-only was passed. Review this, then re-run the same command " +
			"without --render-only to stage it.",
	}, nil
}

func knowledgeStagedPropose(store *knowledge.Store, args []string, stdin io.Reader) (any, error) {
	fs := flag.NewFlagSet("cadre knowledge propose", flag.ContinueOnError)
	input := fs.String("input", "", "a fully-authored record file (frontmatter + body), or - for stdin; "+
		"its status must be 'proposed' and it may not carry a disposition (use import-staged for "+
		"already-dispositioned records)")
	fromFinding := fs.String("from-finding", "", "a JSON file (or - for stdin) with the record's fields; "+
		"id, content_digest, and status are generated")
	renderOnly := fs.Bool("render-only", false, "validate and print the record that would be staged, "+
		"without writing it")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if (*input == "") == (*fromFinding == "") {
		return nil, errors.New("propose requires exactly one of --input or --from-finding")
	}

	if *fromFinding != "" {
		raw, err := knowledgeStagedReadSource(*fromFinding, stdin)
		if err != nil {
			return nil, err
		}
		var finding map[string]any
		if err := json.Unmarshal([]byte(raw), &finding); err != nil {
			return nil, fmt.Errorf("--from-finding did not contain valid JSON: %w", err)
		}
		frontmatter, body, err := knowledge.BuildStagedRecordFromFinding(finding, time.Now())
		if err != nil {
			return nil, err
		}
		if *renderOnly {
			return knowledgeStagedRenderResult(frontmatter, body)
		}
		return store.PutGeneratedStagedRecord(frontmatter, body)
	}

	text, err := knowledgeStagedReadSource(*input, stdin)
	if err != nil {
		return nil, err
	}
	frontmatter, body, err := knowledge.ParseStagedRecord(text)
	if err != nil {
		return nil, err
	}
	if err := refuseAPreDispositionedRecord(frontmatter); err != nil {
		return nil, err
	}
	if *renderOnly {
		return knowledgeStagedRenderResult(frontmatter, body)
	}
	recordID, err := store.PutStagedRecord(frontmatter, body)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"status":         "staged",
		"id":             recordID,
		"record_status":  frontmatter["status"],
		"content_digest": frontmatter["content_digest"],
		"note": "Staged for knowledge-store-steward disposition. Staging is not ingestion: nothing is " +
			"retrievable until a steward accepts this record and it is ingested.",
	}, nil
}

// ---------------------------------------------------------------------------
// show-staged
// ---------------------------------------------------------------------------

func knowledgeStagedShow(store *knowledge.Store, args []string) (any, error) {
	fs := flag.NewFlagSet("cadre knowledge show-staged", flag.ContinueOnError)
	recordID := fs.String("id", "", "the staged record's id (required)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if *recordID == "" {
		return nil, errors.New("show-staged requires --id")
	}
	frontmatter, body, err := store.GetStagedRecord(*recordID)
	if err != nil {
		return nil, err
	}
	text, err := knowledge.SerializeStagedRecord(frontmatter, body)
	if err != nil {
		return nil, err
	}
	history, err := store.StagedHistory(*recordID)
	if err != nil {
		return nil, err
	}
	// import_authorizations is empty for every record staged here through
	// `propose`. It is non-empty only for one this store admitted
	// already-dispositioned, where the decision's own decided_by says who
	// decided and this says who authorized letting that decision in.
	authorizations, err := store.StagedImportAuthorizations(*recordID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"id":                    *recordID,
		"frontmatter":           frontmatter,
		"body":                  body,
		"text":                  text,
		"disposition_history":   history,
		"import_authorizations": authorizations,
	}, nil
}

// ---------------------------------------------------------------------------
// import-staged
// ---------------------------------------------------------------------------

type knowledgeStagedParsedImport struct {
	name        string
	frontmatter map[string]any
	body        string
	history     []knowledge.DispositionEntry
}

// knowledgeStagedImport stages every record file in a directory, atomically
// across the batch.
//
// Migration is the intended use, so a partial import is the wrong outcome: a
// batch that half-succeeds leaves the operator unable to tell which records
// made it without diffing. Every file is validated first, and the batch is
// written only if all of them pass.
//
// SEPARATION CHECK 3 of 4 (roster/knowledge-store/SECURITY.md), which is two
// refusals with different strengths:
//
//   - **Any dispositioned record in the batch requires --authorized-by.** Once
//     `propose` refused pre-dispositioned records, this became the only route
//     by which a decision the store never watched being made can still enter
//     it. That is a legitimate need -- re-importing an exported corpus, moving
//     a store between machines -- but it is not a proposal, and treating it as
//     one is how the self-approval `propose` blocks would simply relocate here.
//     A batch of purely 'proposed' records imports with no ceremony: it is
//     equivalent to a series of `propose` calls and grants nobody anything.
//     The *requirement* to pass the flag is name-independent; the name itself
//     is asserted, not verified -- so it is persisted per admitted record
//     rather than only echoed back, because attributable means recorded.
//   - **A self-approved record is refused outright, authorization or not.** A
//     named human can vouch for a decision the store did not witness; nobody
//     can vouch for a record whose stager and decider are the same actor,
//     because that is not a decision at all. This covers the record's own
//     disposition and every entry of an imported history sidecar, since an
//     earlier self-decision hidden behind a legitimate latest one is the same
//     laundering with an extra step.
func knowledgeStagedImport(store *knowledge.Store, args []string) (any, error) {
	fs := flag.NewFlagSet("cadre knowledge import-staged", flag.ContinueOnError)
	directory := fs.String("directory", "", "directory of staged-record .md files (required)")
	authorizedBy := fs.String("authorized-by", "", "required when any record in the batch carries a "+
		"disposition: the authorized human accountable for admitting decisions this store never saw made")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if *directory == "" {
		return nil, errors.New("import-staged requires --directory")
	}
	info, err := os.Stat(*directory)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("not a directory: %s", *directory)
	}

	sources, err := filepath.Glob(filepath.Join(*directory, "*.md"))
	if err != nil {
		return nil, fmt.Errorf("cannot list %s: %w", *directory, err)
	}
	sort.Strings(sources)
	// README.md is skipped because a directory of records may also carry a
	// README explaining what it is. Only this one name is skipped; any other
	// unparseable file is still a loud failure, because silently ignoring
	// files in a migration is how a partial corpus arrives looking complete.
	filtered := sources[:0]
	for _, path := range sources {
		if filepath.Base(path) != "README.md" {
			filtered = append(filtered, path)
		}
	}
	sources = filtered
	if len(sources) == 0 {
		return nil, fmt.Errorf("no .md staged-record files found in %s", *directory)
	}

	parsed, dispositioned, err := knowledgeStagedParseImportBatch(store, sources)
	if err != nil {
		return nil, err
	}

	// Whitespace is not a name: a gate that "   " satisfies records a blank as
	// the accountable human.
	authorizer := strings.TrimSpace(*authorizedBy)
	if len(dispositioned) > 0 && authorizer == "" {
		listed := dispositioned
		more := ""
		if len(listed) > 5 {
			more = fmt.Sprintf(" (and %d more)", len(listed)-5)
			listed = listed[:5]
		}
		return nil, fmt.Errorf(
			"%d record(s) in this batch already carry a steward's disposition: %s%s. Importing them "+
				"admits decisions this store never saw made, so it requires --authorized-by naming the "+
				"human accountable for that. A batch of purely 'proposed' records needs no authorization. "+
				"Nothing was imported", len(dispositioned), strings.Join(listed, ", "), more)
	}

	var imported []string
	restored := 0
	for _, entry := range parsed {
		recordID, err := store.PutStagedRecord(entry.frontmatter, entry.body)
		if err != nil {
			return nil, err
		}
		imported = append(imported, recordID)
		if len(entry.history) > 0 {
			rows, err := store.PutStagedHistory(recordID, entry.history)
			if err != nil {
				return nil, err
			}
			restored += rows
		}
		if _, hasDisposition := entry.frontmatter["disposition"]; hasDisposition {
			// Written after the record, so an authorization row never
			// describes an admission that failed. authorizer is non-empty
			// here: the gate above refuses this whole batch otherwise.
			if err := store.RecordStagedImportAuthorization(
				recordID,
				knowledge.StagedString(entry.frontmatter, "content_digest"),
				knowledge.StagedString(entry.frontmatter, "status"),
				authorizer, *directory); err != nil {
				return nil, err
			}
		}
	}
	sort.Strings(imported)

	result := map[string]any{
		"status": "imported",
		"count":  len(imported),
		"ids":    imported,
		// Rows written, not sidecars read: a sidecar whose history the store
		// already holds is a no-op, so 0 here after a re-import means "nothing
		// to restore", not "nothing was restored".
		"disposition_history_rows_restored": restored,
	}
	if len(dispositioned) > 0 {
		result["dispositioned"] = dispositioned
		result["authorized_by"] = authorizer
		result["authorization_recorded"] = true
	}
	return result, nil
}

func knowledgeStagedParseImportBatch(
	store *knowledge.Store, sources []string,
) ([]knowledgeStagedParsedImport, []string, error) {
	var parsed []knowledgeStagedParsedImport
	var dispositioned []string

	for _, path := range sources {
		name := filepath.Base(path)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: cannot be read: %w", name, err)
		}
		frontmatter, body, err := knowledge.ParseStagedRecord(string(data))
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", name, err)
		}
		if findings := knowledge.ValidateStagedRecord(frontmatter, body); len(findings) > 0 {
			return nil, nil, fmt.Errorf("%s: %s", name, strings.Join(findings, "; "))
		}
		if _, hasDisposition := frontmatter["disposition"]; hasDisposition {
			if knowledge.StagedRecordIsSelfApproved(frontmatter) {
				return nil, nil, fmt.Errorf(
					"%s: %q both staged and dispositioned this record. Importing cannot launder a "+
						"self-approval, and no --authorized-by permits it: authorship and approval "+
						"separation is why an agent may write to this store at all. Nothing in the batch "+
						"was imported", name, knowledge.StagedDecidedBy(frontmatter))
			}
			dispositioned = append(dispositioned, knowledge.StagedString(frontmatter, "id"))
		}
		history, err := knowledgeStagedLoadExportedHistory(store, path, frontmatter)
		if err != nil {
			return nil, nil, err
		}
		parsed = append(parsed, knowledgeStagedParsedImport{
			name: name, frontmatter: frontmatter, body: body, history: history,
		})
	}
	sort.Strings(dispositioned)
	return parsed, dispositioned, nil
}

// knowledgeStagedLoadExportedHistory reads the `<id>.history.json` sidecar
// beside a record file, validated, or returns nil.
//
// Validation happens here, in the batch's pre-write pass, so a malformed or
// contradictory sidecar refuses the import before any record is written --
// PutStagedHistory re-checks, but reaching its check mid-batch would already
// have written earlier records.
//
// An absent sidecar is not an error: a record may have been dispositioned
// before the history table existed. But an absent sidecar does not license
// contradicting history the store *does* hold, so the record is validated
// against that instead. Otherwise importing a hand-amended `.md` over a
// dispositioned record would leave it disagreeing with its own retained audit
// trail -- amending a disposition is `disposition-staged`'s job, which appends
// rather than overwrites.
func knowledgeStagedLoadExportedHistory(
	store *knowledge.Store, path string, frontmatter map[string]any,
) ([]knowledge.DispositionEntry, error) {
	recordID := knowledge.StagedString(frontmatter, "id")
	sidecarPath := filepath.Join(filepath.Dir(path), recordID+".history.json")
	sidecarName := filepath.Base(sidecarPath)

	data, err := os.ReadFile(sidecarPath)
	if err == nil {
		history, err := knowledge.DecodeStagedHistory(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w. This file is a record's disposition history as written by "+
				"an export; nothing in the batch was imported", sidecarName, err)
		}
		if findings := knowledge.ValidateStagedHistory(frontmatter, history); len(findings) > 0 {
			return nil, fmt.Errorf("%s: %s. Nothing was imported", sidecarName, strings.Join(findings, "; "))
		}
		stagedBy := knowledge.StagedString(frontmatter, "staged_by")
		for _, entry := range history {
			decidedBy, _ := entry["decided_by"].(string)
			if decidedBy != "" && decidedBy == stagedBy {
				return nil, fmt.Errorf(
					"%s: %q both staged this record and dispositioned it at history entry %v. Importing "+
						"cannot launder a self-approval through a history sidecar any more than through a "+
						"record's own disposition. Nothing in the batch was imported",
					sidecarName, decidedBy, entry["sequence"])
			}
		}
		return knowledge.StagedHistoryEntries(history), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s: cannot be read: %w", sidecarName, err)
	}

	retained, err := store.StagedHistory(recordID)
	if err != nil && !errors.Is(err, knowledge.ErrStagedRecordNotFound) {
		return nil, err
	}
	if len(retained) > 0 {
		loose := make([]map[string]any, 0, len(retained))
		for _, entry := range retained {
			loose = append(loose, map[string]any{
				"sequence":               entry.Sequence,
				"action":                 entry.Action,
				"reason":                 entry.Reason,
				"classification_used":    entry.ClassificationUsed,
				"diverged_from_proposal": entry.DivergedFromProposal,
				"decided_by":             entry.DecidedBy,
				"decided_at":             entry.DecidedAt,
			})
		}
		if findings := knowledge.ValidateStagedHistory(frontmatter, loose); len(findings) > 0 {
			return nil, fmt.Errorf(
				"%s: this store already holds a disposition history for %q that the record being "+
					"imported contradicts -- %s. Use disposition-staged to amend a disposition, which "+
					"appends to that history instead of contradicting it. Nothing was imported",
				filepath.Base(path), recordID, strings.Join(findings, "; "))
		}
	}
	return nil, nil
}

// ---------------------------------------------------------------------------
// disposition-staged
// ---------------------------------------------------------------------------

func knowledgeStagedDisposition(store *knowledge.Store, args []string) (any, error) {
	fs := flag.NewFlagSet("cadre knowledge disposition-staged", flag.ContinueOnError)
	recordID := fs.String("id", "", "the staged record's id (required)")
	action := fs.String("action", "", "accepted, rejected, or deferred (required)")
	reason := fs.String("reason", "", "why this decision was made (required)")
	classificationUsed := fs.String("classification-used", "", "the classification actually applied (required)")
	decidedBy := fs.String("decided-by", "", "the steward deciding; must not be the record's staged_by (required)")
	diverged := fs.Bool("diverged-from-proposal", false, "the classification actually applied differs from the one proposed")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	for name, value := range map[string]string{
		"--id": *recordID, "--action": *action, "--reason": *reason,
		"--classification-used": *classificationUsed, "--decided-by": *decidedBy,
	} {
		if value == "" {
			return nil, fmt.Errorf("disposition-staged requires %s", name)
		}
	}
	switch *action {
	case "accepted", "rejected", "deferred":
	default:
		return nil, fmt.Errorf("disposition-staged --action must be one of accepted, rejected, deferred; got %q", *action)
	}

	return store.DispositionStagedRecord(*recordID, knowledge.DispositionInput{
		Action:               *action,
		Reason:               *reason,
		ClassificationUsed:   *classificationUsed,
		DivergedFromProposal: *diverged,
		DecidedBy:            *decidedBy,
	})
}

// ---------------------------------------------------------------------------
// ingest-accepted
// ---------------------------------------------------------------------------

// stagedIDList collects a repeatable --id flag.
type stagedIDList []string

func (l *stagedIDList) String() string     { return strings.Join(*l, ",") }
func (l *stagedIDList) Set(v string) error { *l = append(*l, v); return nil }

// knowledgeStagedIngestAccepted makes steward-accepted records retrievable.
//
// It takes no --decided-by and no --authorized-by, deliberately and by a
// standing decision: the steward decision is already made, and its
// authorship/approval separation was already enforced at
// `disposition-staged`. This executes that decision; it does not take one. A
// human-approval gate was tried here in an earlier session, caught nothing,
// and was removed -- do not reintroduce it. The refusals this command does
// apply (see knowledge.stagedIngestRefusal) exist to stop executing a decision
// that was never validly taken, which is a different thing.
func knowledgeStagedIngestAccepted(
	store *knowledge.Store, cfg *knowledge.Config, args []string,
) (any, error) {
	fs := flag.NewFlagSet("cadre knowledge ingest-accepted", flag.ContinueOnError)
	var ids stagedIDList
	fs.Var(&ids, "id", "ingest only this record; repeatable. Omit to ingest every accepted record.")
	dryRun := fs.Bool("dry-run", false, "report what would be ingested and refused, without writing")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	options := knowledge.IngestAcceptedOptions{RecordIDs: ids, DryRun: *dryRun}
	if !*dryRun {
		// The corpus is opened through the same governed view the read path
		// uses, so a record cannot be written with vectors the store's other
		// content will never be comparable against.
		corpus, err := openGovernedCorpus(cfg)
		if err != nil {
			return nil, err
		}
		defer func() { _ = corpus.Close() }()
		options.Corpus = corpus
	}
	return store.IngestAcceptedStagedRecords(options)
}

// openGovernedCorpus opens the recall store an accepted record is written to.
func openGovernedCorpus(cfg *knowledge.Config) (*retrieval.Governed, error) {
	provider, code := resolveEmbedder(knowledgeEnv{cfg: cfg}, cfg.Embedding.Provider, "ingest-accepted")
	if code != 0 {
		return nil, fmt.Errorf("cannot resolve the embedding provider")
	}
	return retrieval.OpenForIngest(retrieval.Options{
		Database:     cfg.Database,
		EmbedderName: cfg.Embedding.Provider,
		Model:        cfg.Embedding.Model,
		Dimensions:   cfg.Embedding.Dimensions,
	}, provider)
}

// ---------------------------------------------------------------------------
// delete-staged
// ---------------------------------------------------------------------------

func knowledgeStagedDelete(store *knowledge.Store, args []string) (any, error) {
	fs := flag.NewFlagSet("cadre knowledge delete-staged", flag.ContinueOnError)
	recordID := fs.String("id", "", "the staged record's id (required)")
	reason := fs.String("reason", "", "why the record is being deleted (required)")
	deletedBy := fs.String("deleted-by", "", "who is deleting it (required)")
	authorizedBy := fs.String("authorized-by", "", "required to delete an accepted record: the authorized "+
		"human reversing the decision")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	for name, value := range map[string]string{
		"--id": *recordID, "--reason": *reason, "--deleted-by": *deletedBy,
	} {
		if value == "" {
			return nil, fmt.Errorf("delete-staged requires %s", name)
		}
	}
	return store.DeleteStagedRecord(*recordID, knowledge.DeleteStagedInput{
		Reason:       *reason,
		DeletedBy:    *deletedBy,
		AuthorizedBy: *authorizedBy,
	})
}
