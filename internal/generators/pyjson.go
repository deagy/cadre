package generators

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Python-compatible JSON emission.
//
// Every JSON artifact this generator writes is compared byte-for-byte against
// the Python generator's output by CI, so the encoding has to match
// `json.dumps(obj, indent=2)` exactly rather than merely being valid JSON.
// Two differences from encoding/json matter:
//
//   - Python defaults to ensure_ascii=True, escaping every character outside
//     0x20..0x7e as \uXXXX (surrogate pairs above the BMP). Go emits UTF-8.
//     The role instructions embedded into Codex .toml wrappers are full of
//     em dashes, so this is not a corner case.
//   - Go escapes <, > and & as </>/& by default; Python does
//     not. Go also sorts map keys, while Python preserves insertion order.
//
// pyJSONString and the orderedJSON tree below give exact control over both.

// pyJSONString renders s the way Python's json.dumps(s) does with the default
// ensure_ascii=True.
func pyJSONString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r >= 0x20 && r <= 0x7e {
				b.WriteRune(r)
				continue
			}
			if r < 0x10000 {
				fmt.Fprintf(&b, `\u%04x`, r)
				continue
			}
			hi, lo := utf16.EncodeRune(r)
			fmt.Fprintf(&b, `\u%04x\u%04x`, hi, lo)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// orderedJSON is an insertion-ordered JSON object, matching Python dict
// iteration order.
type orderedJSON struct {
	keys   []string
	values map[string]any
}

func newOrderedJSON() *orderedJSON {
	return &orderedJSON{values: map[string]any{}}
}

// Set appends key (or replaces its value in place, keeping the original
// position, exactly as a Python dict assignment does).
func (o *orderedJSON) Set(key string, value any) *orderedJSON {
	if _, exists := o.values[key]; !exists {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
	return o
}

func (o *orderedJSON) Get(key string) (any, bool) {
	value, ok := o.values[key]
	return value, ok
}

func (o *orderedJSON) Keys() []string { return o.keys }

// pyJSONDumps renders value the way Python's json.dumps(value, indent=2) does,
// without a trailing newline. Supported value types: *orderedJSON, []any,
// []string, string, int, float64, bool, nil.
func pyJSONDumps(value any, indent int) string {
	var b strings.Builder
	writePyJSON(&b, value, indent)
	return b.String()
}

func writePyJSON(b *strings.Builder, value any, depth int) {
	pad := strings.Repeat("  ", depth)
	inner := strings.Repeat("  ", depth+1)
	switch typed := value.(type) {
	case nil:
		b.WriteString("null")
	case string:
		b.WriteString(pyJSONString(typed))
	case bool:
		b.WriteString(strconv.FormatBool(typed))
	case int:
		b.WriteString(strconv.Itoa(typed))
	case float64:
		b.WriteString(strconv.FormatFloat(typed, 'g', -1, 64))
	case json.Number:
		// Re-emit the literal token, so a round-tripped `1` stays `1` rather
		// than becoming `1.0` the way a float64 detour would render it.
		b.WriteString(typed.String())
	case []string:
		anys := make([]any, len(typed))
		for i, item := range typed {
			anys[i] = item
		}
		writePyJSON(b, anys, depth)
	case []any:
		if len(typed) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteString("[\n")
		for i, item := range typed {
			b.WriteString(inner)
			writePyJSON(b, item, depth+1)
			if i < len(typed)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		b.WriteString(pad)
		b.WriteString("]")
	case *orderedJSON:
		if len(typed.keys) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteString("{\n")
		for i, key := range typed.keys {
			b.WriteString(inner)
			b.WriteString(pyJSONString(key))
			b.WriteString(": ")
			writePyJSON(b, typed.values[key], depth+1)
			if i < len(typed.keys)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		b.WriteString(pad)
		b.WriteString("}")
	default:
		panic(fmt.Sprintf("pyJSONDumps: unsupported type %T", value))
	}
}

// parseOrderedJSON decodes a JSON object into an insertion-ordered tree, so a
// document read from disk can be re-emitted with its original key order --
// encoding/json's map decoding loses that, and the packaged copies of
// agent-catalog.json and provider.json must stay byte-comparable with the
// Python generator's output.
func parseOrderedJSON(data []byte) (*orderedJSON, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != '{' {
		return nil, fmt.Errorf("top-level content must be a JSON object")
	}
	return decodeOrderedObject(decoder)
}

func decodeOrderedObject(decoder *json.Decoder) (*orderedJSON, error) {
	object := newOrderedJSON()
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
		object.Set(key, value)
	}
	if _, err := decoder.Token(); err != nil { // consume '}'
		return nil, err
	}
	return object, nil
}

func decodeOrderedValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("unexpected end of JSON input")
		}
		return nil, err
	}
	if delimiter, ok := token.(json.Delim); ok {
		switch delimiter {
		case '{':
			return decodeOrderedObject(decoder)
		case '[':
			items := []any{}
			for decoder.More() {
				item, err := decodeOrderedValue(decoder)
				if err != nil {
					return nil, err
				}
				items = append(items, item)
			}
			if _, err := decoder.Token(); err != nil { // consume ']'
				return nil, err
			}
			return items, nil
		default:
			return nil, fmt.Errorf("unexpected delimiter %q", delimiter)
		}
	}
	return token, nil
}
