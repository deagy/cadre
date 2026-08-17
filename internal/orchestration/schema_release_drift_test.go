package orchestration

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The emitted plan's field set is a released contract.
//
// selection.schema.json describes what `cadre select` prints. It is closed
// (additionalProperties: false) and it ships inside the wheel, so a consumer
// validates against the copy they installed. Adding, removing or retyping a
// field without bumping schema_version breaks that consumer silently: their
// validator rejects a plan the CLI considers correct, and nothing here failed.
//
// The baseline is the schema exactly as committed at the last release tag,
// read with `git show`. Comparing against a rebuilt or hand-maintained copy
// would compare the code with itself.
//
// Ported from roster/orchestration/test/test_schema_release_drift.py.

const schemaRelativePath = "roster/orchestration/selection.schema.json"

var releaseTagPattern = regexp.MustCompile(`^plugin-v(\d+)\.(\d+)\.(\d+)$`)

func repoRootForDrift(t *testing.T) string {
	t.Helper()
	root := checkoutRoot(t)
	if _, err := os.Stat(filepath.Join(root, schemaRelativePath)); err != nil {
		t.Skipf("not running inside a source checkout: %v", err)
	}
	return root
}

func gitOutput(t *testing.T, root string, args ...string) (string, bool) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	out, err := command.Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

// releaseTagsNewestFirst returns every plugin-vX.Y.Z tag in version order.
//
// Not `git describe` (it matches the inherited bare v* tags), not a string
// sort (lexically 0.9.0 beats 0.18.0), and not commit date (a patch release
// may be tagged after a later minor).
func releaseTagsNewestFirst(t *testing.T, root string) []string {
	t.Helper()
	listing, ok := gitOutput(t, root, "tag", "--list", "plugin-v*")
	if !ok {
		return nil
	}
	type versioned struct {
		major, minor, patch int
		tag                 string
	}
	var found []versioned
	for _, line := range strings.Split(listing, "\n") {
		match := releaseTagPattern.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil {
			continue
		}
		major, _ := strconv.Atoi(match[1])
		minor, _ := strconv.Atoi(match[2])
		patch, _ := strconv.Atoi(match[3])
		found = append(found, versioned{major, minor, patch, strings.TrimSpace(line)})
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].major != found[j].major {
			return found[i].major > found[j].major
		}
		if found[i].minor != found[j].minor {
			return found[i].minor > found[j].minor
		}
		return found[i].patch > found[j].patch
	})
	tags := make([]string, 0, len(found))
	for _, entry := range found {
		tags = append(tags, entry.tag)
	}
	return tags
}

// schemaAtTag reads the schema exactly as committed at tag. Absent when the
// file did not exist there, or when a shallow clone lacks the object.
func schemaAtTag(t *testing.T, root, tag string) (map[string]any, bool) {
	t.Helper()
	blob, ok := gitOutput(t, root, "show", tag+":"+schemaRelativePath)
	if !ok {
		return nil, false
	}
	var schema map[string]any
	if err := json.Unmarshal([]byte(blob), &schema); err != nil {
		return nil, false
	}
	return schema, true
}

func currentSchema(t *testing.T, root string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, schemaRelativePath))
	if err != nil {
		t.Fatalf("reading the schema: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("the schema does not parse: %v", err)
	}
	return schema
}

func jsonTypeName(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64, int:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "any"
}

// typeSignature is the comparable type of one resolved subschema.
//
// Derived from `type` when present, else from the JSON type of `const` or
// `enum` values -- so schema_version's const moving 6 -> 7 is not a retype,
// and widening an enum with another string is not either. Values are
// deliberately outside the signature: the rule is about the emitted field
// *set*, and pulling value constraints in would make the version bump itself
// register as drift.
func typeSignature(node map[string]any) string {
	if declared, present := node["type"]; present {
		switch typed := declared.(type) {
		case string:
			return typed
		case []any:
			var names []string
			for _, name := range typed {
				names = append(names, fmt.Sprint(name))
			}
			sort.Strings(names)
			return strings.Join(names, "|")
		}
	}
	if constant, present := node["const"]; present {
		return jsonTypeName(constant)
	}
	if enum, present := node["enum"]; present {
		if values, ok := enum.([]any); ok {
			unique := map[string]bool{}
			for _, value := range values {
				unique[jsonTypeName(value)] = true
			}
			var names []string
			for name := range unique {
				names = append(names, name)
			}
			sort.Strings(names)
			return strings.Join(names, "|")
		}
	}
	return "any"
}

// resolveRef inlines a local #/$defs/... reference.
//
// Sibling keys win over the target's -- 2020-12 allows $ref siblings, and
// matched_routes uses one for `description`. A reference already on the path
// resolves to a marker rather than recursing forever.
func resolveRef(node, schema map[string]any, seen map[string]bool) (map[string]any, map[string]bool) {
	rawRef, present := node["$ref"]
	ref, isString := rawRef.(string)
	if !present || !isString || !strings.HasPrefix(ref, "#/") {
		return node, seen
	}
	if seen[ref] {
		return map[string]any{"type": "<recursive>"}, seen
	}

	var target any = schema
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		object, ok := target.(map[string]any)
		if !ok {
			return map[string]any{"type": "<unresolvable-ref>"}, seen
		}
		next, present := object[part]
		if !present {
			return map[string]any{"type": "<unresolvable-ref>"}, seen
		}
		target = next
	}
	resolved, ok := target.(map[string]any)
	if !ok {
		return map[string]any{"type": "<unresolvable-ref>"}, seen
	}

	merged := map[string]any{}
	for key, value := range resolved {
		merged[key] = value
	}
	for key, value := range node {
		if key != "$ref" {
			merged[key] = value
		}
	}
	widened := map[string]bool{ref: true}
	for key := range seen {
		widened[key] = true
	}
	return merged, widened
}

// emittedFieldSignatures maps every property a plan can carry to its
// comparable type. Paths are dotted, with [] for array items:
// inputs.task, context_packs[].content_hash, teams[].instances.min.
func emittedFieldSignatures(schema map[string]any) map[string]string {
	signatures := map[string]string{}

	var walk func(node any, path string, seen map[string]bool, depth int)
	walk = func(node any, path string, seen map[string]bool, depth int) {
		object, ok := node.(map[string]any)
		if !ok || depth > 64 {
			return
		}
		object, seen = resolveRef(object, schema, seen)
		if path != "" {
			signatures[path] = typeSignature(object)
		}
		if properties, ok := object["properties"].(map[string]any); ok {
			for name, subschema := range properties {
				child := name
				if path != "" {
					child = path + "." + name
				}
				walk(subschema, child, seen, depth+1)
			}
		}
		if items, ok := object["items"].(map[string]any); ok {
			walk(items, path+"[]", seen, depth+1)
		}
		if prefixItems, ok := object["prefixItems"].([]any); ok {
			for index, subschema := range prefixItems {
				walk(subschema, fmt.Sprintf("%s[%d]", path, index), seen, depth+1)
			}
		}
	}
	walk(schema, "", map[string]bool{}, 0)
	return signatures
}

func schemaVersionOf(schema map[string]any) any {
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return nil
	}
	field, ok := properties["schema_version"].(map[string]any)
	if !ok {
		return nil
	}
	if constant, present := field["const"]; present {
		return constant
	}
	return nil
}

type fieldDifferences struct {
	added, removed, retyped []string
}

func (d fieldDifferences) any() bool {
	return len(d.added)+len(d.removed)+len(d.retyped) > 0
}

func (d fieldDifferences) describe() string {
	var parts []string
	for label, items := range map[string][]string{
		"added": d.added, "removed": d.removed, "retyped": d.retyped,
	} {
		if len(items) > 0 {
			sorted := append([]string{}, items...)
			sort.Strings(sorted)
			parts = append(parts, label+": "+strings.Join(sorted, ", "))
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}

func compareFieldSets(previous, current map[string]string) fieldDifferences {
	var differences fieldDifferences
	for path, signature := range current {
		previousSignature, present := previous[path]
		switch {
		case !present:
			differences.added = append(differences.added, path)
		case previousSignature != signature:
			differences.retyped = append(differences.retyped,
				fmt.Sprintf("%s (%s -> %s)", path, previousSignature, signature))
		}
	}
	for path := range previous {
		if _, present := current[path]; !present {
			differences.removed = append(differences.removed, path)
		}
	}
	return differences
}

// --- the extractor's own behaviour ---

func TestTheSignatureWalksNestedObjectsArrayItemsAndRefs(t *testing.T) {
	schema := map[string]any{
		"$defs": map[string]any{
			"route": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string"},
				},
			},
		},
		"type": "object",
		"properties": map[string]any{
			"inputs": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task": map[string]any{"type": "string"},
				},
			},
			"matched_routes": map[string]any{
				"type":  "array",
				"items": map[string]any{"$ref": "#/$defs/route"},
			},
		},
	}
	signatures := emittedFieldSignatures(schema)
	for path, want := range map[string]string{
		"inputs":              "object",
		"inputs.task":         "string",
		"matched_routes":      "array",
		"matched_routes[]":    "object",
		"matched_routes[].id": "string",
	} {
		if got := signatures[path]; got != want {
			t.Errorf("%s = %q, want %q (all: %v)", path, got, want, signatures)
		}
	}
}

func TestResolutionTerminatesOnASelfReferentialDefinition(t *testing.T) {
	// A schema that refers to itself is legal and does occur. Without the
	// seen-set this walk does not return.
	schema := map[string]any{
		"$defs": map[string]any{
			"node": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"child": map[string]any{"$ref": "#/$defs/node"},
				},
			},
		},
		"type": "object",
		"properties": map[string]any{
			"tree": map[string]any{"$ref": "#/$defs/node"},
		},
	}
	signatures := emittedFieldSignatures(schema)
	if signatures["tree"] != "object" {
		t.Errorf("tree = %q, want object", signatures["tree"])
	}
	if signatures["tree.child"] != "<recursive>" {
		t.Errorf("tree.child = %q, want <recursive>", signatures["tree.child"])
	}
}

func TestASiblingKeyWinsOverTheReferencedTarget(t *testing.T) {
	// 2020-12 allows $ref siblings, and the shipped schema uses one for
	// description. A resolver that let the target win would silently discard
	// the sibling.
	schema := map[string]any{
		"$defs": map[string]any{
			"thing": map[string]any{"type": "string"},
		},
		"type": "object",
		"properties": map[string]any{
			"overridden": map[string]any{"$ref": "#/$defs/thing", "type": "integer"},
		},
	}
	if got := emittedFieldSignatures(schema)["overridden"]; got != "integer" {
		t.Errorf("overridden = %q, want integer (the sibling)", got)
	}
}

func TestTheSignatureIgnoresValuesSoAVersionBumpIsNotDrift(t *testing.T) {
	// schema_version's const moving is the very thing that authorises a change.
	// If the signature included values, every bump would report itself as
	// drift and the guard would be unusable.
	before := map[string]any{"type": "object", "properties": map[string]any{
		"schema_version": map[string]any{"const": float64(6)},
		"status":         map[string]any{"enum": []any{"ok", "needs-triage"}},
	}}
	after := map[string]any{"type": "object", "properties": map[string]any{
		"schema_version": map[string]any{"const": float64(7)},
		"status":         map[string]any{"enum": []any{"ok", "needs-triage", "blocked"}},
	}}
	if differences := compareFieldSets(
		emittedFieldSignatures(before), emittedFieldSignatures(after)); differences.any() {
		t.Errorf("a const bump and an enum widening registered as drift: %s",
			differences.describe())
	}
}

// --- drift detection ---

func TestAChangedFieldSetIsDetectedAndAReworkIsNot(t *testing.T) {
	base := func() map[string]any {
		return map[string]any{"type": "object", "properties": map[string]any{
			"schema_version": map[string]any{"const": float64(6)},
			"inputs": map[string]any{"type": "object", "properties": map[string]any{
				"task": map[string]any{"type": "string", "description": "the task"},
			}},
			"agents": map[string]any{"type": "array", "items": map[string]any{
				"type": "object", "properties": map[string]any{
					"id": map[string]any{"type": "string"},
				}}},
		}}
	}
	properties := func(schema map[string]any) map[string]any {
		return schema["properties"].(map[string]any)
	}

	for _, testCase := range []struct {
		name   string
		change func(schema map[string]any)
		drift  bool
	}{
		{"a new top-level property", func(s map[string]any) {
			properties(s)["extra"] = map[string]any{"type": "string"}
		}, true},
		{"a new nested property", func(s map[string]any) {
			inputs := properties(s)["inputs"].(map[string]any)
			inputs["properties"].(map[string]any)["files"] = map[string]any{"type": "array"}
		}, true},
		{"a new array-item property", func(s map[string]any) {
			items := properties(s)["agents"].(map[string]any)["items"].(map[string]any)
			items["properties"].(map[string]any)["model"] = map[string]any{"type": "string"}
		}, true},
		{"a removed property", func(s map[string]any) {
			delete(properties(s), "agents")
		}, true},
		{"a retyped property", func(s map[string]any) {
			inputs := properties(s)["inputs"].(map[string]any)
			inputs["properties"].(map[string]any)["task"] = map[string]any{"type": "array"}
		}, true},
		{"a reworded description", func(s map[string]any) {
			inputs := properties(s)["inputs"].(map[string]any)
			task := inputs["properties"].(map[string]any)["task"].(map[string]any)
			task["description"] = "a completely different sentence"
		}, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			previous := emittedFieldSignatures(base())
			changed := base()
			testCase.change(changed)
			differences := compareFieldSets(previous, emittedFieldSignatures(changed))
			if differences.any() != testCase.drift {
				t.Errorf("drift = %v, want %v (%s)",
					differences.any(), testCase.drift, differences.describe())
			}
		})
	}
}

// --- the release guard itself ---

func TestReleaseTagsAreOrderedByVersionNotByString(t *testing.T) {
	// Which tag is "latest" decides what the baseline is. A string sort puts
	// plugin-v0.9.0 above plugin-v0.23.3, so the guard would compare against a
	// schema from long before the last release and report drift that was
	// already shipped -- or miss drift that was not.
	root := repoRootForDrift(t)
	tags := releaseTagsNewestFirst(t, root)
	if len(tags) < 2 {
		t.Skip("fewer than two release tags are reachable")
	}
	parse := func(tag string) (int, int, int) {
		m := releaseTagPattern.FindStringSubmatch(tag)
		major, _ := strconv.Atoi(m[1])
		minor, _ := strconv.Atoi(m[2])
		patch, _ := strconv.Atoi(m[3])
		return major, minor, patch
	}
	for index := 1; index < len(tags); index++ {
		aMajor, aMinor, aPatch := parse(tags[index-1])
		bMajor, bMinor, bPatch := parse(tags[index])
		if aMajor < bMajor ||
			(aMajor == bMajor && aMinor < bMinor) ||
			(aMajor == bMajor && aMinor == bMinor && aPatch < bPatch) {
			t.Fatalf("tags are not in descending version order: %s before %s",
				tags[index-1], tags[index])
		}
	}
	// And the ordering is not merely lexical, which it would coincide with if
	// no two versions disagreed. Assert the corpus actually distinguishes them.
	byString := append([]string{}, tags...)
	sort.Sort(sort.Reverse(sort.StringSlice(byString)))
	if byString[0] == tags[0] && len(tags) > 5 {
		t.Logf("note: version order and string order agree on the newest tag (%s); "+
			"this check is weaker than it looks until a 0.9-vs-0.23 pair exists", tags[0])
	}
}

// On this guard being inert mid-cycle.
//
// A bump authorises every field change in that release cycle: once
// schema_version has moved past the released baseline, further drift is by
// definition already declared. So the guard fails only between a release and
// the first bump after it -- which is exactly when an undeclared change is
// dangerous, and is why falsifying it requires resetting schema_version to the
// baseline's value first. Mutating the field set alone proves nothing while a
// bump is already in place.
func TestTheEmittedFieldSetDidNotChangeWithoutAVersionBump(t *testing.T) {
	root := repoRootForDrift(t)
	tags := releaseTagsNewestFirst(t, root)
	if len(tags) == 0 {
		requireGuard(t, "no plugin-v* release tag is reachable")
		return
	}
	baseline, ok := schemaAtTag(t, root, tags[0])
	if !ok {
		requireGuard(t, "the schema at "+tags[0]+" is not reachable (shallow clone?)")
		return
	}

	current := currentSchema(t, root)
	previousSignatures := emittedFieldSignatures(baseline)
	currentSignatures := emittedFieldSignatures(current)
	if len(previousSignatures) < 10 || len(currentSignatures) < 10 {
		t.Fatalf("extracted %d baseline and %d current fields; the walk is broken",
			len(previousSignatures), len(currentSignatures))
	}

	differences := compareFieldSets(previousSignatures, currentSignatures)
	if !differences.any() {
		return
	}
	previousVersion := schemaVersionOf(baseline)
	currentVersion := schemaVersionOf(current)
	if fmt.Sprint(previousVersion) == fmt.Sprint(currentVersion) {
		t.Errorf("the emitted field set changed since %s without bumping "+
			"schema_version (still %v):\n  %s\n\n"+
			"The schema is closed and ships inside the wheel, so a consumer "+
			"validating against the copy they installed will reject a plan this "+
			"CLI considers correct.",
			tags[0], currentVersion, differences.describe())
	}
	t.Logf("field set changed since %s and schema_version moved %v -> %v: %s",
		tags[0], previousVersion, currentVersion, differences.describe())
}

func TestSchemaVersionNeverMovesBackwards(t *testing.T) {
	root := repoRootForDrift(t)
	tags := releaseTagsNewestFirst(t, root)
	if len(tags) == 0 {
		requireGuard(t, "no plugin-v* release tag is reachable")
		return
	}
	baseline, ok := schemaAtTag(t, root, tags[0])
	if !ok {
		requireGuard(t, "the schema at "+tags[0]+" is not reachable")
		return
	}
	previous, previousOK := schemaVersionOf(baseline).(float64)
	current, currentOK := schemaVersionOf(currentSchema(t, root)).(float64)
	if !previousOK || !currentOK {
		t.Skip("schema_version is not a numeric const on both sides")
	}
	if current < previous {
		t.Errorf("schema_version moved backwards: %v at %s, %v now",
			previous, tags[0], current)
	}
}

// requireGuard skips locally but fails under CI, where the baseline must be
// reachable. A guard that silently skips in the one place it is meant to run
// is the failure this whole file exists to prevent, one level up.
func requireGuard(t *testing.T, reason string) {
	t.Helper()
	if os.Getenv("GITHUB_ACTIONS") != "" ||
		os.Getenv("CADRE_REQUIRE_SCHEMA_RELEASE_GUARD") == "1" {
		t.Fatalf("the schema release guard could not run: %s\n"+
			"Under CI this must not be skipped -- fetch tags and history "+
			"(actions/checkout with fetch-depth: 0 and fetch-tags: true).", reason)
	}
	t.Skipf("%s; set CADRE_REQUIRE_SCHEMA_RELEASE_GUARD=1 to make this fatal", reason)
}
