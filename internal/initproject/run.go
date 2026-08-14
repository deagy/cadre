// run.go ports init_project.py's run_init: the top-level orchestration of
// answer loading, --set application, plan_writes, diff preview,
// --force write execution with post-write verification
// (THREAT-MODEL-HARDENING-4), audit trail flushing, and --repair /
// --print-answers modes.
package initproject

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/deagy/cadre/cli/internal/config"
	"gopkg.in/yaml.v3"
)

// LoadAnswers reads and validates a non-interactive answer file.
func LoadAnswers(path string) (map[string]any, error) {
	text, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if err := yaml.Unmarshal(text, &data); err != nil {
		return nil, initErrorf("%s: invalid YAML: %v", path, err)
	}
	if data == nil {
		return nil, initErrorf("%s: answer file root must be a mapping", path)
	}
	version, _ := data["schema_version"].(int)
	if versionF, ok := data["schema_version"].(float64); ok {
		version = int(versionF)
	}
	if version != 1 {
		return nil, initErrorf("%s: unsupported schema_version %v, expected 1", path, data["schema_version"])
	}
	return data, nil
}

// RunInitOptions mirrors run_init's argparse.Namespace fields.
type RunInitOptions struct {
	TargetPath        string
	Target            string
	Stack             string
	AnswersPath       string
	Interactive       bool
	SetValues         []string
	Sections          string // comma-separated; default is all
	DryRun            bool
	Force             bool
	Repair            bool
	Apply             bool
	PrintAnswers      bool
	SharedDefaultsDir string // repoRoot/roster/shared
	AuditLogPath      string // "" means DefaultAuditLogPath()
}

// RunInit is the full non-interactive cadre init flow. Returns the process
// exit code, matching run_init's contract exactly (0 success, 1 a runtime
// failure, 2 a usage/argument problem).
func RunInit(opts RunInitOptions, stdout, stderr io.Writer) int {
	if opts.Apply && !opts.Repair {
		writeln(stderr, "cadre init: --apply is valid only with --repair")
		return 2
	}
	if opts.Repair {
		incompatible := repairIncompatibleFlags(opts)
		if len(incompatible) > 0 {
			writeln(stderr, "cadre init: --repair does not accept change-planning options: "+strings.Join(incompatible, ", "))
			return 2
		}
	}
	if opts.AnswersPath != "" && opts.Interactive {
		writeln(stderr, "cadre init: --answers and --interactive are mutually exclusive")
		return 2
	}
	if opts.Interactive && len(opts.SetValues) > 0 {
		writeln(stderr, "cadre init: --set and --interactive are mutually exclusive")
		return 2
	}
	if opts.Interactive {
		// Scoped out -- see package doc. Fails closed rather than silently
		// doing something different from the Python original's interactive
		// questionnaire.
		writeln(stderr, "cadre init: --interactive is not yet implemented in the Go CLI; "+
			"use --answers <file> or --set [REGION:]PATH=VALUE instead")
		return 2
	}

	targetRoot, err := ResolveTargetRoot(ResolveTargetRootOptions{Target: opts.Target, PositionalTarget: opts.TargetPath})
	if err != nil {
		writef(stderr, "cadre init: %v\n", err)
		return 2
	}
	if err := RefuseIfSelfCheckout(targetRoot); err != nil {
		writef(stderr, "cadre init: %v\n", err)
		return 1
	}
	if info, err := os.Stat(targetRoot); err != nil || !info.IsDir() {
		writef(stderr, "cadre init: project root does not exist or is not a directory: %s\n", targetRoot)
		return 1
	}

	if opts.Repair {
		state, repairErrors := InspectRepairState(targetRoot, opts.SharedDefaultsDir)
		for _, item := range state {
			writef(stdout, "cadre init: repair: %s: %s\n", item.Status, item.Path)
		}
		if len(repairErrors) > 0 {
			for _, e := range repairErrors {
				writef(stderr, "cadre init: repair blocked: %s\n", e)
			}
			writeln(stderr, "cadre init: repair made no changes")
			return 1
		}
		writeln(stdout, "cadre init: repair inspection complete; no automatic overlay rewrite is safe")
		return 0
	}

	sectionsCSV := opts.Sections
	if sectionsCSV == "" {
		sectionsCSV = strings.Join(AllSections, ",")
	}
	var sections []string
	for _, s := range strings.Split(sectionsCSV, ",") {
		if s = strings.TrimSpace(s); s != "" {
			sections = append(sections, s)
		}
	}
	var unknownSections []string
	for _, s := range sections {
		if !stringInSlice(s, AllSections) {
			unknownSections = append(unknownSections, s)
		}
	}
	if len(unknownSections) > 0 {
		writef(stderr, "cadre init: unknown --sections value(s): %v\n", unknownSections)
		return 2
	}

	var preset map[string]any
	if opts.Stack != "" {
		p, err := LoadStackPreset(opts.SharedDefaultsDir, opts.Stack)
		if err != nil {
			writef(stderr, "cadre init: %v\n", err)
			return 1
		}
		preset = p
	}

	defaultsMode := opts.AnswersPath == ""
	var answers map[string]any
	if defaultsMode {
		answers = map[string]any{
			"schema_version":  1,
			"target_project":  targetRoot,
			"stack_preset":    opts.Stack,
			"field_decisions": map[string]any{},
		}
		answers = MergeAnswersWithPreset(answers, preset)
	} else {
		a, err := LoadAnswers(opts.AnswersPath)
		if err != nil {
			writef(stderr, "cadre init: %v\n", err)
			return 1
		}
		answers = MergeAnswersWithPreset(a, preset)
	}

	if len(opts.SetValues) > 0 {
		a, err := ApplySetOverrides(opts.SharedDefaultsDir, answers, opts.SetValues, sections)
		if err != nil {
			writef(stderr, "cadre init: %v\n", err)
			return 1
		}
		answers = a
	}
	if defaultsMode {
		answers = SynthesizePresetFieldDecisions(opts.SharedDefaultsDir, answers)
	}

	result, errs := PlanWrites(targetRoot, opts.SharedDefaultsDir, answers, sections)

	if opts.PrintAnswers {
		// Finding A: echoed only AFTER PlanWrites has validated everything,
		// with rg_b_autonomy/rg_b_guardrails_addendum redacted to their
		// post-validation accepted/rejected status. Deliberately still
		// printed even when errs is non-empty below -- an operator needs to
		// see WHY a run was rejected.
		redacted := RedactAnswersForEcho(answers, result)
		out, err := yaml.Marshal(redacted)
		if err == nil {
			writeraw(stdout, string(out))
		}
	}

	var auditEntries []AuditEntry
	for _, r := range result.RejectedGuardrails {
		auditEntries = append(auditEntries, NewAuditEntry("rejected", "governance", "cloud-guardrails.md addendum bullet", r.Reason, r.Bullet, false))
	}
	for _, r := range result.RejectedAutonomy {
		// finding 2: only ever logged as a hash (kind="rejected" forces
		// hash-only); the raw value never reaches stderr or any other
		// printed output.
		auditEntries = append(auditEntries, NewAuditEntry("rejected", "governance", "agent-autonomy.yaml field "+r.FieldPath,
			"autonomy overlay rejected (unrecognized value or a loosening of the default)", anyToString(r.Value), true))
	}

	if len(errs) > 0 {
		for _, e := range errs {
			writef(stderr, "cadre init: error: %s\n", e)
		}
		auditEntries = append(auditEntries, NewAuditEntry("rejected", "stack", "run", "run failed validation; no writes performed (A-005 fail-closed)", "", false))
		logPath, _ := AppendAuditEntries(auditEntries, opts.AuditLogPath)
		writef(stderr, "cadre init: no files written (fail-closed); audit log: %s\n", logPath)
		return 1
	}

	force := opts.Force && !opts.DryRun
	logPath := ""
	writeErr := func() error {
		for _, planned := range result.Planned {
			existingText, hasExisting := readExistingOverlayText(targetRoot, planned.Filename)
			dest := existingOverlayPath(targetRoot, planned.Filename)
			diff := unifiedDiff(existingText, hasExisting, planned.Content, dest)
			verb := "would write"
			if force {
				verb = "writing"
			}
			writef(stdout, "--- %s: %s ---\n", verb, dest)
			if diff != "" {
				writeln(stdout, diff)
			} else {
				writeln(stdout, "(no change)")
			}
			detail := "dry-run preview"
			if force {
				detail = "written"
			}
			auditEntries = append(auditEntries, NewAuditEntry("accepted", planned.Category, planned.Filename, detail, "", false))

			if force {
				writtenPath, err := WriteOverlay(targetRoot, planned.Filename, planned.Content)
				if err != nil {
					return err
				}
				// THREAT-MODEL-HARDENING-4: re-read through
				// ResolveSharedConfig and confirm it matches what we
				// intended to write.
				if _, err := config.ResolveSharedConfig(opts.SharedDefaultsDir, planned.Filename, targetRoot); err != nil {
					return err
				}
				writtenText, err := os.ReadFile(writtenPath)
				if err != nil {
					return err
				}
				if string(writtenText) != planned.Content {
					verificationErr := initErrorf("post-write verification failed for %s", writtenPath)
					auditEntries = append(auditEntries, NewAuditEntry("rejected", planned.Category, planned.Filename, verificationErr.Error(), "", false))
					return verificationErr
				}
				auditEntries = append(auditEntries, NewAuditEntry("written", planned.Category, planned.Filename,
					"post-write resolve_shared_config verification passed", string(writtenText), true))
				result.Written = append(result.Written, writtenPath)
			}
		}
		return nil
	}()
	logPath, _ = AppendAuditEntries(auditEntries, opts.AuditLogPath)
	result.AuditLogPath = logPath

	if writeErr != nil {
		writef(stderr, "cadre init: %v\n", writeErr)
		return 1
	}

	switch {
	case len(result.Planned) == 0:
		writef(stdout, "cadre init: no overlays needed -- %s keeps every shipped default "+
			"(override individual fields with --set, or answer the full flow with --interactive); audit log: %s\n",
			targetRoot, logPath)
	case force:
		writef(stdout, "cadre init: wrote %d file(s); audit log: %s\n", len(result.Written), logPath)
	default:
		writef(stdout, "cadre init: dry-run only, no files written (pass --force to write); audit log: %s\n", logPath)
	}
	return 0
}

// writef/writeln/writeraw write CLI output and deliberately discard the
// write error, mirroring internal/cli's dispatcher.go writef/writeln: a
// stdout/stderr write failure here has no secondary channel to escalate to
// beyond the exit code this function already computes.
func writef(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func writeln(w io.Writer, args ...any) {
	_, _ = fmt.Fprintln(w, args...)
}

func writeraw(w io.Writer, s string) {
	_, _ = fmt.Fprint(w, s)
}

func repairIncompatibleFlags(opts RunInitOptions) []string {
	var out []string
	if opts.Force {
		out = append(out, "--force")
	}
	if opts.DryRun {
		out = append(out, "--dry-run")
	}
	if opts.AnswersPath != "" {
		out = append(out, "--answers")
	}
	if opts.Interactive {
		out = append(out, "--interactive")
	}
	if opts.Stack != "" {
		out = append(out, "--stack")
	}
	if len(opts.SetValues) > 0 {
		out = append(out, "--set")
	}
	if opts.PrintAnswers {
		out = append(out, "--print-answers")
	}
	if opts.Sections != "" && opts.Sections != strings.Join(AllSections, ",") {
		out = append(out, "--sections")
	}
	return out
}
