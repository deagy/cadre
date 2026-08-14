package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/deagy/cadre/cli/internal/contextstore"
)

// ContextCmd is the `cadre context` command: init_project.py's sibling for
// the agent context store, a faithful port of
// roster/context-store/src/cli.py.
func ContextCmd(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: cadre context <init|put|get|list|search|reindex|export|promote|prune-audit|drop|expire|stats> [options]")
		return 2
	}
	command := args[0]
	rest := args[1:]

	switch command {
	case "init":
		return contextInit(rest)
	case "stats":
		return contextStats(rest)
	case "expire":
		return contextExpire(rest)
	case "prune-audit":
		return contextPruneAudit(rest)
	case "reindex":
		return contextReindex(rest)
	case "put":
		return contextPut(rest)
	case "get":
		return contextGet(rest)
	case "list":
		return contextList(rest)
	case "search":
		return contextSearch(rest)
	case "export":
		return contextExport(rest)
	case "promote":
		return contextPromote(rest)
	case "drop":
		return contextDrop(rest)
	default:
		fmt.Fprintf(os.Stderr, "cadre context: unknown command %q\n", command)
		return 2
	}
}

func contextError(err error) int {
	fmt.Fprintf(os.Stderr, "cadre context: %v\n", err)
	return 1
}

func contextEmit(payload any) int {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return contextError(err)
	}
	fmt.Println(string(data))
	return 0
}

// callerFlags is add_caller() from cli.py: every read/write's attribution.
type callerFlags struct {
	agent          *string
	taskID         *string
	classification *string
	source         *string
}

func addCallerFlags(fs *flag.FlagSet) *callerFlags {
	return &callerFlags{
		agent:          fs.String("agent", "", "acting role id (required)"),
		taskID:         fs.String("task-id", "", "task identifier (required)"),
		classification: fs.String("classification", "", "classification level (required)"),
		source:         fs.String("source", "", "project/repository identifier"),
	}
}

// enforceScope requires an explicit project scope against the shared
// global store. Deliberately stricter than the knowledge store: there is
// no --all-sources equivalent here, and none is planned.
func enforceScope(tier, source string) error {
	if tier != contextstore.TierGlobalFallback {
		return nil
	}
	if source == "" {
		return fmt.Errorf(
			"a project scope is required against the shared global context store: pass " +
				"--source <project-identifier>. Unlike `cadre knowledge`, there is no " +
				"--all-sources equivalent -- cross-project reads of unreviewed agent working " +
				"notes are not an offered mode. Use a project-local " +
				".agents/context-store/config.json for a real partition")
	}
	return nil
}

func resolvedSource(source string) string {
	if source == "" {
		return "local"
	}
	return source
}

func readInputSource(path string) (string, error) {
	if path == "" || path == "-" {
		data, err := io.ReadAll(os.Stdin)
		return string(data), err
	}
	data, err := os.ReadFile(path)
	return string(data), err
}

func contextInit(args []string) int {
	fs := flag.NewFlagSet("cadre context init", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "explicit config file path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, tier, err := contextstore.LoadConfig(*configPath)
	if err != nil {
		return contextError(err)
	}
	db, err := contextstore.OpenStore(cfg.Database, true)
	if err != nil {
		return contextError(err)
	}
	_ = db.Close()
	return contextEmit(map[string]any{"initialized": cfg.Database, "tier": tier})
}

func contextStats(args []string) int {
	fs := flag.NewFlagSet("cadre context stats", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "explicit config file path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, tier, err := contextstore.LoadConfig(*configPath)
	if err != nil {
		return contextError(err)
	}
	db, err := contextstore.OpenStore(cfg.Database, true)
	if err != nil {
		return contextError(err)
	}
	defer func() { _ = db.Close() }()
	stats, err := contextstore.GetStoreStats(db)
	if err != nil {
		return contextError(err)
	}
	return contextEmit(struct {
		Database string `json:"database"`
		Tier     string `json:"tier"`
		*contextstore.StoreStats
	}{cfg.Database, tier, stats})
}

func contextExpire(args []string) int {
	fs := flag.NewFlagSet("cadre context expire", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "explicit config file path")
	asOf := fs.String("as-of", "", "ISO-8601 moment to evaluate expiry against")
	dryRun := fs.Bool("dry-run", false, "report without destroying")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, _, err := contextstore.LoadConfig(*configPath)
	if err != nil {
		return contextError(err)
	}
	// --dry-run must not sweep on open, or the report would describe
	// entries the act of asking had already destroyed.
	db, err := contextstore.OpenStore(cfg.Database, !*dryRun)
	if err != nil {
		return contextError(err)
	}
	defer func() { _ = db.Close() }()

	if *dryRun {
		rows, err := contextstore.ExpiredRows(db, *asOf)
		if err != nil {
			return contextError(err)
		}
		type expiredSummary struct {
			Handle         string `json:"handle"`
			Classification string `json:"classification"`
			ByteLength     int    `json:"byte_length"`
			ExpiresAt      string `json:"expires_at"`
		}
		summaries := make([]expiredSummary, len(rows))
		for i, row := range rows {
			summaries[i] = expiredSummary{row.Handle, row.Classification, row.ByteLength, row.ExpiresAt}
		}
		return contextEmit(map[string]any{
			"dry_run": true, "as_of": nullableIfEmpty(*asOf), "expired": summaries, "count": len(summaries),
		})
	}
	swept, err := contextstore.SweepExpired(db, *asOf)
	if err != nil {
		return contextError(err)
	}
	return contextEmit(map[string]any{
		"dry_run": false, "as_of": nullableIfEmpty(*asOf), "swept": swept, "count": len(swept),
	})
}

func nullableIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func contextPruneAudit(args []string) int {
	fs := flag.NewFlagSet("cadre context prune-audit", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "explicit config file path")
	olderThanDays := fs.Int("older-than-days", 0, "required: prune rows older than this many days")
	acknowledgeLoss := fs.Bool("acknowledge-loss", false, "required: pruning audit rows destroys accountability, it is not hygiene")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, _, err := contextstore.LoadConfig(*configPath)
	if err != nil {
		return contextError(err)
	}
	db, err := contextstore.OpenStore(cfg.Database, true)
	if err != nil {
		return contextError(err)
	}
	defer func() { _ = db.Close() }()
	result, err := contextstore.PruneAudit(db, contextstore.PruneAuditOptions{
		OlderThanDays: *olderThanDays, AcknowledgeLoss: *acknowledgeLoss,
	})
	if err != nil {
		return contextError(err)
	}
	return contextEmit(result)
}

func contextReindex(args []string) int {
	fs := flag.NewFlagSet("cadre context reindex", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "explicit config file path")
	force := fs.Bool("force", false, "rebuild every entry, not only those with no vectors under the current settings")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, _, err := contextstore.LoadConfig(*configPath)
	if err != nil {
		return contextError(err)
	}
	db, err := contextstore.OpenStore(cfg.Database, true)
	if err != nil {
		return contextError(err)
	}
	defer func() { _ = db.Close() }()
	result, err := contextstore.ReindexEntries(db, cfg, *force)
	if err != nil {
		return contextError(err)
	}
	return contextEmit(result)
}

func openScopedStore(configPath string) (*contextstore.Config, string, error) {
	cfg, tier, err := contextstore.LoadConfig(configPath)
	if err != nil {
		return nil, "", err
	}
	return cfg, tier, nil
}

func contextPut(args []string) int {
	fs := flag.NewFlagSet("cadre context put", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "explicit config file path")
	label := fs.String("label", "", "required")
	input := fs.String("input", "", "file to read, or '-' for stdin (default: stdin)")
	scope := fs.String("scope", "agent", "agent|dispatch|project")
	dispatchID := fs.String("dispatch-id", "", "")
	var tags, derivedFrom stringSliceFlag
	fs.Var(&tags, "tag", "repeatable")
	fs.Var(&derivedFrom, "derived-from", "repeatable")
	ttlDays := fs.Int("ttl-days", 0, "0 means unset (use the scope's default)")
	caller := addCallerFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, tier, err := openScopedStore(*configPath)
	if err != nil {
		return contextError(err)
	}
	if err := enforceScope(tier, *caller.source); err != nil {
		return contextError(err)
	}
	source := resolvedSource(*caller.source)
	content, err := readInputSource(*input)
	if err != nil {
		return contextError(err)
	}
	db, err := contextstore.OpenStore(cfg.Database, true)
	if err != nil {
		return contextError(err)
	}
	defer func() { _ = db.Close() }()

	var ttlOverride *int
	if *ttlDays != 0 {
		ttlOverride = ttlDays
	}
	result, err := contextstore.PutEntry(db, cfg, contextstore.PutOptions{
		Label: *label, Content: content, Scope: *scope, DispatchID: *dispatchID,
		Tags: []string(tags), DerivedFrom: []string(derivedFrom), TTLDaysOverride: ttlOverride,
		Agent: *caller.agent, TaskID: *caller.taskID, Classification: *caller.classification, Source: source,
	})
	if err != nil {
		return contextError(err)
	}
	return contextEmit(result)
}

func contextGet(args []string) int {
	fs := flag.NewFlagSet("cadre context get", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "explicit config file path")
	handle := fs.String("handle", "", "required")
	dispatchID := fs.String("dispatch-id", "", "")
	caller := addCallerFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, tier, err := openScopedStore(*configPath)
	if err != nil {
		return contextError(err)
	}
	if err := enforceScope(tier, *caller.source); err != nil {
		return contextError(err)
	}
	source := resolvedSource(*caller.source)
	db, err := contextstore.OpenStore(cfg.Database, true)
	if err != nil {
		return contextError(err)
	}
	defer func() { _ = db.Close() }()
	result, err := contextstore.GetEntry(db, contextstore.GetOptions{
		Handle: *handle,
		CallerOptions: contextstore.CallerOptions{
			Agent: *caller.agent, TaskID: *caller.taskID, Classification: *caller.classification,
			Source: source, DispatchID: *dispatchID,
		},
	})
	if err != nil {
		return contextError(err)
	}
	return contextEmit(result)
}

func contextList(args []string) int {
	fs := flag.NewFlagSet("cadre context list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "explicit config file path")
	scope := fs.String("scope", "", "agent|dispatch|project")
	dispatchID := fs.String("dispatch-id", "", "caller's own dispatch identity")
	filterDispatchID := fs.String("filter-dispatch-id", "", "narrow results to one dispatch")
	filterAgent := fs.String("filter-agent", "", "")
	filterTaskID := fs.String("filter-task-id", "", "")
	var tags stringSliceFlag
	fs.Var(&tags, "tag", "repeatable")
	top := fs.String("top", "", "")
	caller := addCallerFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, tier, err := openScopedStore(*configPath)
	if err != nil {
		return contextError(err)
	}
	if err := enforceScope(tier, *caller.source); err != nil {
		return contextError(err)
	}
	source := resolvedSource(*caller.source)
	db, err := contextstore.OpenStore(cfg.Database, true)
	if err != nil {
		return contextError(err)
	}
	defer func() { _ = db.Close() }()
	result, err := contextstore.ListEntries(db, contextstore.ListOptions{
		CallerOptions: contextstore.CallerOptions{
			Agent: *caller.agent, TaskID: *caller.taskID, Classification: *caller.classification,
			Source: source, DispatchID: *dispatchID, Scope: *scope,
		},
		FilterDispatchID: *filterDispatchID, FilterAgent: *filterAgent, FilterTaskID: *filterTaskID,
		Tags: []string(tags), Top: *top,
	})
	if err != nil {
		return contextError(err)
	}
	return contextEmit(result)
}

func contextSearch(args []string) int {
	fs := flag.NewFlagSet("cadre context search", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "explicit config file path")
	query := fs.String("query", "", "required")
	scope := fs.String("scope", "", "agent|dispatch|project")
	dispatchID := fs.String("dispatch-id", "", "")
	top := fs.String("top", "", "")
	caller := addCallerFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, tier, err := openScopedStore(*configPath)
	if err != nil {
		return contextError(err)
	}
	if err := enforceScope(tier, *caller.source); err != nil {
		return contextError(err)
	}
	source := resolvedSource(*caller.source)
	db, err := contextstore.OpenStore(cfg.Database, true)
	if err != nil {
		return contextError(err)
	}
	defer func() { _ = db.Close() }()
	result, err := contextstore.SearchEntries(db, cfg, *query, contextstore.SearchOptions{
		CallerOptions: contextstore.CallerOptions{
			Agent: *caller.agent, TaskID: *caller.taskID, Classification: *caller.classification,
			Source: source, DispatchID: *dispatchID, Scope: *scope,
		},
		Top: *top,
	})
	if err != nil {
		return contextError(err)
	}
	return contextEmit(result)
}

func contextExport(args []string) int {
	fs := flag.NewFlagSet("cadre context export", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "explicit config file path")
	output := fs.String("output", "", "required")
	var handles stringSliceFlag
	fs.Var(&handles, "handle", "repeatable")
	scope := fs.String("scope", "", "agent|dispatch|project")
	dispatchID := fs.String("dispatch-id", "", "")
	filterDispatchID := fs.String("filter-dispatch-id", "", "")
	acknowledgeCommit := fs.Bool("acknowledge-commit", false, "required for confidential entries")
	includeUntrusted := fs.Bool("include-untrusted", false, "required to export entries flagged untrusted_inputs")
	caller := addCallerFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, tier, err := openScopedStore(*configPath)
	if err != nil {
		return contextError(err)
	}
	if err := enforceScope(tier, *caller.source); err != nil {
		return contextError(err)
	}
	source := resolvedSource(*caller.source)
	db, err := contextstore.OpenStore(cfg.Database, true)
	if err != nil {
		return contextError(err)
	}
	defer func() { _ = db.Close() }()
	result, err := contextstore.ExportEntries(db, contextstore.ExportOptions{
		CallerOptions: contextstore.CallerOptions{
			Agent: *caller.agent, TaskID: *caller.taskID, Classification: *caller.classification,
			Source: source, DispatchID: *dispatchID, Scope: *scope,
		},
		Output: *output, Handles: []string(handles), FilterDispatchID: *filterDispatchID,
		AcknowledgeCommit: *acknowledgeCommit, IncludeUntrusted: *includeUntrusted,
	})
	if err != nil {
		return contextError(err)
	}
	return contextEmit(result)
}

func contextPromote(args []string) int {
	fs := flag.NewFlagSet("cadre context promote", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "explicit config file path")
	handle := fs.String("handle", "", "required")
	dispatchID := fs.String("dispatch-id", "", "")
	artifact := fs.String("artifact", "", "required")
	revision := fs.String("revision", "", "required")
	sensitivityNotes := fs.String("sensitivity-notes", "", "required")
	conflictsOrStaleness := fs.String("conflicts-or-staleness", "", "required")
	recommendedAction := fs.String("recommended-action", "", "required: ingest|update|reclassify|defer")
	findingOnly := fs.Bool("finding-only", false, "print just the finding object")
	caller := addCallerFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, tier, err := openScopedStore(*configPath)
	if err != nil {
		return contextError(err)
	}
	if err := enforceScope(tier, *caller.source); err != nil {
		return contextError(err)
	}
	source := resolvedSource(*caller.source)
	db, err := contextstore.OpenStore(cfg.Database, true)
	if err != nil {
		return contextError(err)
	}
	defer func() { _ = db.Close() }()
	result, err := contextstore.PromoteEntry(db, contextstore.PromoteOptions{
		CallerOptions: contextstore.CallerOptions{
			Agent: *caller.agent, TaskID: *caller.taskID, Classification: *caller.classification,
			Source: source, DispatchID: *dispatchID,
		},
		Handle: *handle, Artifact: *artifact, Revision: *revision, SensitivityNotes: *sensitivityNotes,
		ConflictsOrStaleness: *conflictsOrStaleness, RecommendedAction: *recommendedAction,
	})
	if err != nil {
		return contextError(err)
	}
	if result.UntrustedInstructionRisk {
		fmt.Fprintln(os.Stderr,
			"cadre context: this entry derives from material that tripped injection detection, so "+
				"the proposal carries untrusted_instruction_risk=true. The knowledge store's "+
				"staged-record contract will defer it automatically; that is the intended path, not "+
				"a failure.")
	}
	if *findingOnly {
		return contextEmit(result.Finding)
	}
	return contextEmit(result)
}

func contextDrop(args []string) int {
	fs := flag.NewFlagSet("cadre context drop", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "explicit config file path")
	handle := fs.String("handle", "", "required")
	reason := fs.String("reason", "", "required")
	dispatchID := fs.String("dispatch-id", "", "")
	caller := addCallerFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, tier, err := openScopedStore(*configPath)
	if err != nil {
		return contextError(err)
	}
	if err := enforceScope(tier, *caller.source); err != nil {
		return contextError(err)
	}
	source := resolvedSource(*caller.source)
	db, err := contextstore.OpenStore(cfg.Database, true)
	if err != nil {
		return contextError(err)
	}
	defer func() { _ = db.Close() }()
	result, err := contextstore.DropEntry(db, contextstore.DropOptions{
		CallerOptions: contextstore.CallerOptions{
			Agent: *caller.agent, TaskID: *caller.taskID, Classification: *caller.classification,
			Source: source, DispatchID: *dispatchID,
		},
		Handle: *handle, Reason: *reason,
	})
	if err != nil {
		return contextError(err)
	}
	return contextEmit(result)
}
