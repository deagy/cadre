package kernel

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// JSON Schema validation of the two documents a task directory holds.
//
// The hand-written checks in validate_runrecord.go know what the lifecycle
// means; the schemas know what the documents are shaped like. Both are needed,
// and the schema half is the one that catches a field that is a string where
// an object was expected -- the kind of malformation that would otherwise make
// every semantic check silently read nil and conclude nothing is wrong.
//
// Format assertion is on. A `date-time` that is not one is not a stylistic
// matter here: gate timestamps are when an approval happened, and the semantic
// checks that compare them are only meaningful if they parse.
//
// One honest limitation, stated because a differential test has to account for
// it: the message *text* a schema violation produces is the validating
// library's own, and Go's library does not word its messages the way Python's
// does. The path a violation is reported at is the same; the sentence after it
// is not. Everything a human greps for -- which file, which field -- matches.

var (
	compiledSchemas   = map[string]*jsonschema.Schema{}
	compiledSchemasMu sync.Mutex
)

// contractSchema compiles an embedded contract schema, once per process.
func contractSchema(name string) (*jsonschema.Schema, error) {
	compiledSchemasMu.Lock()
	defer compiledSchemasMu.Unlock()
	if schema, cached := compiledSchemas[name]; cached {
		return schema, nil
	}

	raw, err := EmbeddedContract(name)
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat = true
	url := "https://agentic-sdlc.invalid/contracts/" + name
	if err := compiler.AddResource(url, bytes.NewReader(raw)); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	schema, err := compiler.Compile(url)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	compiledSchemas[name] = schema
	return schema, nil
}

// SchemaViolations validates a document against an embedded contract schema
// and returns one message per violation, each prefixed "schema <location>: ".
//
// The location is dotted rather than a JSON pointer -- lifecycle_gates.0.status
// -- because that is what a reader of this kernel's output has always seen.
func SchemaViolations(document any, contractName string) ([]string, error) {
	schema, err := contractSchema(contractName)
	if err != nil {
		return nil, err
	}
	err = schema.Validate(document)
	if err == nil {
		return nil, nil
	}
	var validation *jsonschema.ValidationError
	if !errors.As(err, &validation) {
		return []string{fmt.Sprintf("schema <root>: %s", err.Error())}, nil
	}

	var violations []string
	collectViolations(validation, &violations)
	sort.Strings(violations)
	return violations, nil
}

// collectViolations walks to the leaves. An aggregate node (a failing anyOf,
// or the root itself) has no message worth printing on its own -- the useful
// sentence is always at the bottom.
func collectViolations(violation *jsonschema.ValidationError, out *[]string) {
	if len(violation.Causes) == 0 {
		*out = append(*out, fmt.Sprintf("schema %s: %s",
			dottedLocation(violation.InstanceLocation), violation.Message))
		return
	}
	for _, cause := range violation.Causes {
		collectViolations(cause, out)
	}
}

// dottedLocation converts a JSON pointer to the dotted form.
func dottedLocation(pointer string) string {
	trimmed := strings.TrimPrefix(pointer, "/")
	if trimmed == "" {
		return "<root>"
	}
	parts := strings.Split(trimmed, "/")
	for index, part := range parts {
		// Unescape in pointer order: ~1 before ~0, or a literal "~1" in a key
		// would be mangled.
		parts[index] = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
	}
	return strings.Join(parts, ".")
}
