package kernel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/deagy/cadre/cli/internal/canonicaljson"
)

// Echoing a JSON document back the way the Python kernel does.
//
// Several subcommands read a sidecar file and print it. That sounds like a
// copy, and it is not: Python parses the file and re-serializes it with
// `json.dumps(value, indent=2)`, so the output is the document's *values*
// re-rendered -- key order preserved from the file, non-ASCII escaped,
// separators normalised. A caller diffing two kernels' output would see every
// line change if this were done with Go's own encoder, which sorts object
// keys and emits raw UTF-8.
//
// So: decode into a value that remembers key order, then render with Python's
// escaping. Go's map[string]any cannot be used as the intermediate, because
// the ordering is lost at decode time and no amount of care at encode time
// gets it back.

// orderedObject is a JSON object that remembers the order its keys arrived in.
type orderedObject struct {
	keys   []string
	values map[string]any
}

func (o *orderedObject) set(key string, value any) {
	if _, seen := o.values[key]; !seen {
		o.keys = append(o.keys, key)
	}
	// A repeated key keeps its first position and its last value, which is
	// what Python's dict does with a duplicate key in a JSON document.
	o.values[key] = value
}

// MarshalJSON lets an ordered object travel through encoding/json intact.
//
// Without it the type marshals as {} -- every field is unexported -- and
// anything that round-trips one through encoding/json silently loses the whole
// document. That is not hypothetical: the profile digest was briefly the
// sha256 of an empty object, which is a valid-looking hash of nothing.
func (o *orderedObject) MarshalJSON() ([]byte, error) {
	var builder strings.Builder
	builder.WriteByte('{')
	for index, key := range o.keys {
		if index > 0 {
			builder.WriteByte(',')
		}
		encodedKey, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		builder.Write(encodedKey)
		builder.WriteByte(':')
		encodedValue, err := json.Marshal(o.values[key])
		if err != nil {
			return nil, err
		}
		builder.Write(encodedValue)
	}
	builder.WriteByte('}')
	return []byte(builder.String()), nil
}

// DecodeOrdered parses JSON, preserving object key order.
//
// Numbers are kept as json.Number so an integer stays an integer: Go's
// default float64 turns 1 into 1 on the way out only by luck, and turns
// 10000000000000000000 into 1e+19.
func DecodeOrdered(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := decodeOrderedValue(decoder)
	if err != nil {
		return nil, err
	}
	// Trailing content is a malformed document, not a document with a
	// postscript.
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing data after JSON value")
	}
	return value, nil
}

func decodeOrderedValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	return decodeOrderedFrom(decoder, token)
}

func decodeOrderedFrom(decoder *json.Decoder, token json.Token) (any, error) {
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := &orderedObject{values: map[string]any{}}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, fmt.Errorf("object key is not a string")
			}
			value, err := decodeOrderedValue(decoder)
			if err != nil {
				return nil, err
			}
			object.set(key, value)
		}
		if _, err := decoder.Token(); err != nil { // consume '}'
			return nil, err
		}
		return object, nil
	case '[':
		list := []any{}
		for decoder.More() {
			value, err := decodeOrderedValue(decoder)
			if err != nil {
				return nil, err
			}
			list = append(list, value)
		}
		if _, err := decoder.Token(); err != nil { // consume ']'
			return nil, err
		}
		return list, nil
	}
	return nil, fmt.Errorf("unexpected delimiter %v", delimiter)
}

// RenderIndented renders a value as `json.dumps(value, indent=2)` does,
// including the trailing newline Python's print adds.
//
// Empty objects and arrays collapse to {} and [], which is Python's behaviour
// and not Go's json.MarshalIndent.
func RenderIndented(value any) string {
	var builder strings.Builder
	writeIndented(&builder, value, 0)
	builder.WriteByte('\n')
	return builder.String()
}

func writeIndented(builder *strings.Builder, value any, depth int) {
	switch typed := value.(type) {
	case nil:
		builder.WriteString("null")
	case bool:
		builder.WriteString(strconv.FormatBool(typed))
	case string:
		canonicaljson.WriteString(builder, typed)
	case json.Number:
		builder.WriteString(typed.String())
	case float64:
		builder.WriteString(strconv.FormatFloat(typed, 'g', -1, 64))
	case int:
		builder.WriteString(strconv.Itoa(typed))
	case *orderedObject:
		writeIndentedObject(builder, typed.keys, func(key string) any {
			return typed.values[key]
		}, depth)
	case map[string]any:
		// Sorted, because an unordered map has no order to preserve and an
		// arbitrary one would differ between runs of this binary, never mind
		// between this binary and Python.
		writeIndentedObject(builder, sortedKeys(typed), func(key string) any {
			return typed[key]
		}, depth)
	case []any:
		if len(typed) == 0 {
			builder.WriteString("[]")
			return
		}
		builder.WriteString("[\n")
		for index, element := range typed {
			if index > 0 {
				builder.WriteString(",\n")
			}
			writeIndent(builder, depth+1)
			writeIndented(builder, element, depth+1)
		}
		builder.WriteByte('\n')
		writeIndent(builder, depth)
		builder.WriteByte(']')
	default:
		// Anything else round-trips through encoding/json first so structs
		// and named types arrive as the shapes above.
		encoded, err := json.Marshal(value)
		if err != nil {
			builder.WriteString("null")
			return
		}
		decoded, err := DecodeOrdered(encoded)
		if err != nil {
			builder.WriteString("null")
			return
		}
		writeIndented(builder, decoded, depth)
	}
}

func writeIndentedObject(
	builder *strings.Builder, keys []string, valueOf func(string) any, depth int,
) {
	writeIndentedObjectWith(builder, keys, valueOf, depth, writeIndented)
}

func writeIndentedObjectWith(
	builder *strings.Builder, keys []string, valueOf func(string) any, depth int,
	writeValue func(*strings.Builder, any, int),
) {
	if len(keys) == 0 {
		builder.WriteString("{}")
		return
	}
	builder.WriteString("{\n")
	for index, key := range keys {
		if index > 0 {
			builder.WriteString(",\n")
		}
		writeIndent(builder, depth+1)
		canonicaljson.WriteString(builder, key)
		builder.WriteString(": ")
		writeValue(builder, valueOf(key), depth+1)
	}
	builder.WriteByte('\n')
	writeIndent(builder, depth)
	builder.WriteByte('}')
}

func writeIndent(builder *strings.Builder, depth int) {
	builder.WriteString(strings.Repeat("  ", depth))
}

func sortedKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// sortedIndentedJSON renders a value as Python's
// json.dumps(value, indent=2, sort_keys=True) does.
//
// The sorted counterpart to RenderIndented, for the ledgers: they are
// rewritten in full on every publication, so a stable key order is what makes
// their diffs readable. Everything else this kernel writes preserves the
// order its author gave it.
func sortedIndentedJSON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoded, err := DecodeOrdered(encoded)
	if err != nil {
		return nil, err
	}
	var builder strings.Builder
	writeSortedIndented(&builder, decoded, 0)
	return []byte(builder.String()), nil
}

func writeSortedIndented(builder *strings.Builder, value any, depth int) {
	switch typed := value.(type) {
	case *orderedObject:
		// The one place an ordered object is deliberately re-sorted.
		writeIndentedObjectWith(builder, sortedKeys(typed.values), func(key string) any {
			return typed.values[key]
		}, depth, writeSortedIndented)
	case []any:
		if len(typed) == 0 {
			builder.WriteString("[]")
			return
		}
		builder.WriteString("[\n")
		for index, element := range typed {
			if index > 0 {
				builder.WriteString(",\n")
			}
			writeIndent(builder, depth+1)
			writeSortedIndented(builder, element, depth+1)
		}
		builder.WriteByte('\n')
		writeIndent(builder, depth)
		builder.WriteByte(']')
	default:
		writeIndented(builder, value, depth)
	}
}
