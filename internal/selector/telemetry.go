package selector

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Selection telemetry: a local, structural-only outcome record appended to a
// JSON-lines file, off unless explicitly asked for.
//
// A record deliberately carries structural facts about the outcome -- which
// routes matched, how many agents in each group, which gates -- and never raw
// task text or file paths, unless the caller opts in a second time. Matched
// routes and risks are reduced to bare ids for exactly that reason: a plan's
// reasons.paths[].file entries are changed-file paths.

const (
	// TelemetrySchemaVersion is the record schema, independent of the plan's.
	TelemetrySchemaVersion = 2

	telemetryEnableEnv      = "CADRE_SELECTION_TELEMETRY"
	telemetryIncludeTaskEnv = "CADRE_SELECTION_TELEMETRY_INCLUDE_TASK"
	telemetryPathEnv        = "CADRE_SELECTION_TELEMETRY_PATH"

	telemetryDefaultRelativePath = ".agents/orchestration/selection-telemetry.jsonl"
)

func telemetryEnvFlag(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// TelemetryIsEnabled reports whether recording was opted into, by flag or env.
func TelemetryIsEnabled(flag bool) bool {
	return flag || telemetryEnvFlag(telemetryEnableEnv)
}

// TelemetryIncludeTask is a second, separate opt-in on top of the first:
// capturing raw task text is a different decision from recording an outcome.
func TelemetryIncludeTask(flag bool) bool {
	return flag || telemetryEnvFlag(telemetryIncludeTaskEnv)
}

// ResolveTelemetryPath prefers an explicit override, then the environment,
// then the project-local default.
func ResolveTelemetryPath(repositoryRoot, override string) string {
	if override != "" {
		return expandUser(override)
	}
	if fromEnv := os.Getenv(telemetryPathEnv); fromEnv != "" {
		return expandUser(fromEnv)
	}
	return filepath.Join(repositoryRoot, filepath.FromSlash(telemetryDefaultRelativePath))
}

func expandUser(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
}

// BuildTelemetryRecord derives a record from a completed plan.
//
// task_id is always included even without includeTask, and is caller-supplied
// free text when an explicit --task-id was passed. Anyone relying on the "no
// raw task content in the base record" property should keep --task-id a short
// opaque identifier rather than a descriptive string.
func BuildTelemetryRecord(plan map[string]any, includeTask bool, now time.Time) map[string]any {
	normalized, err := normalizePlanForText(plan)
	if err != nil {
		normalized = plan
	}

	agents := objectOf(normalized["agents"])
	inputs := objectOf(normalized["inputs"])

	record := map[string]any{
		"schema_version": TelemetrySchemaVersion,
		"recorded_at": strings.Replace(
			now.UTC().Format("2006-01-02T15:04:05.000-07:00"), "+00:00", "Z", 1),
		"task_id":        normalized["task_id"],
		"status":         normalized["status"],
		"workflow":       normalized["workflow"],
		"matched_routes": entryIDs(normalized["matched_routes"]),
		"matched_risks":  entryIDs(normalized["matched_risks"]),
		"classification": inputs["classification"],
		"source_filter":  inputs["source_filter"],
		"agent_counts": map[string]any{
			"primary":   len(anyList(agents["primary"])),
			"reviewers": len(anyList(agents["reviewers"])),
			"support":   len(anyList(agents["support"])),
		},
		"teams":                       entryIDs(normalized["teams"]),
		"lifecycle_tracking_status":   objectOf(normalized["lifecycle_tracking"])["status"],
		"required_quality_gate_count": len(anyList(normalized["required_quality_gates"])),
		"human_gate_count":            len(anyList(normalized["human_gates"])),
	}
	if includeTask {
		record["task"] = inputs["task"]
		files := anyList(inputs["changed_files"])
		if files == nil {
			files = []any{}
		}
		record["changed_files"] = files
	}
	return record
}

// entryIDs reduces a list of objects to their bare ids, skipping anything
// that is not an object -- the shape the Python comprehension produces.
func entryIDs(value any) []any {
	items := anyList(value)
	ids := make([]any, 0, len(items))
	for _, item := range items {
		if object, ok := item.(map[string]any); ok {
			ids = append(ids, object["id"])
		}
	}
	return ids
}

// RecordSelection appends exactly one record for the plan.
//
// Callers gate this behind TelemetryIsEnabled themselves -- this always
// writes when called, by design, so "off by default" lives at the one CLI
// call site rather than being duplicated here.
func RecordSelection(plan map[string]any, repositoryRoot, override string, includeTask bool) (string, error) {
	path := ResolveTelemetryPath(repositoryRoot, override)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}

	record := BuildTelemetryRecord(plan, includeTask, time.Now())
	encoded, err := TelemetryJSON(record)
	if err != nil {
		return "", err
	}

	handle, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	defer func() { _ = handle.Close() }()

	// One Write call, not two. Under concurrent invocations -- a busy CI
	// environment, say -- two separate writes have no atomicity guarantee
	// against each other, while a single write under O_APPEND does for sizes
	// under PIPE_BUF. Coalescing is what actually makes concurrent appends
	// safe here; it is not a buffering detail that could quietly stop
	// applying.
	if _, err := handle.Write(append(encoded, '\n')); err != nil {
		return "", err
	}
	return path, nil
}

// TelemetryJSON is json.dumps(record, sort_keys=True, ensure_ascii=False):
// keys sorted, non-ASCII left as raw UTF-8, and -- because no `separators=`
// argument is passed on the Python side and indent is None -- the *default*
// ", " / ": " spacing rather than the compact form.
//
// Two deliberate differences from CanonicalJSON, which is the other encoder
// in this package and would be the obvious thing to reuse:
//
//   - It does not escape non-ASCII. This is a log a person reads, where an
//     escaped task is strictly worse than the text it came from; CanonicalJSON
//     escapes because it feeds a hash that must be byte-stable.
//   - It emits spaced separators. CanonicalJSON is compact.
//
// Neither changes what the JSON means, which is exactly why getting one wrong
// is easy: a telemetry file appended to by both implementations would parse
// identically and diff on every single line.
func TelemetryJSON(value any) ([]byte, error) {
	normalized, err := normalizeForCanonical(value)
	if err != nil {
		return nil, err
	}
	var builder strings.Builder
	if err := writeTelemetryJSON(&builder, normalized); err != nil {
		return nil, err
	}
	return []byte(builder.String()), nil
}

func writeTelemetryJSON(builder *strings.Builder, value any) error {
	// Everything except string escaping matches the canonical encoder, so
	// this delegates for structure and overrides only the leaf case.
	switch typed := value.(type) {
	case string:
		writeUnicodeString(builder, typed)
		return nil
	case []any:
		builder.WriteByte('[')
		for index, element := range typed {
			if index > 0 {
				builder.WriteString(", ")
			}
			if err := writeTelemetryJSON(builder, element); err != nil {
				return err
			}
		}
		builder.WriteByte(']')
		return nil
	case map[string]any:
		keys := sortedKeys(typed)
		builder.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				builder.WriteString(", ")
			}
			writeUnicodeString(builder, key)
			builder.WriteString(": ")
			if err := writeTelemetryJSON(builder, typed[key]); err != nil {
				return err
			}
		}
		builder.WriteByte('}')
		return nil
	default:
		return writeCanonical(builder, value)
	}
}

// writeUnicodeString escapes what JSON requires and nothing else, leaving
// every non-ASCII rune as itself.
func writeUnicodeString(builder *strings.Builder, value string) {
	builder.WriteByte('"')
	for _, runeValue := range value {
		switch runeValue {
		case '"':
			builder.WriteString(`\"`)
		case '\\':
			builder.WriteString(`\\`)
		case '\b':
			builder.WriteString(`\b`)
		case '\f':
			builder.WriteString(`\f`)
		case '\n':
			builder.WriteString(`\n`)
		case '\r':
			builder.WriteString(`\r`)
		case '\t':
			builder.WriteString(`\t`)
		default:
			if runeValue < 0x20 {
				builder.WriteString(controlEscape(runeValue))
				continue
			}
			builder.WriteRune(runeValue)
		}
	}
	builder.WriteByte('"')
}

func controlEscape(runeValue rune) string {
	const hexDigits = "0123456789abcdef"
	return `\u00` + string([]byte{hexDigits[(runeValue>>4)&0xf], hexDigits[runeValue&0xf]})
}
