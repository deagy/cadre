// Package canonicaljson writes the exact bytes Python's json.dumps produces
// with sort_keys=True and separators=(",", ":").
//
// It lives in its own package because two independent things hash with it --
// the selector, when it fingerprints a dispatch plan, and the kernel, when it
// recomputes that fingerprint during validate -- and those two must agree
// byte for byte or every plan the selector emits is rejected by the kernel
// that reads it. That is not a hypothetical: they disagreed, over which keys
// to exclude, and validate refused every plan cadre select produced.
//
// Sharing the encoder removes one of the two ways they can drift. The other
// -- which keys each excludes -- is not shareable, because the kernel's
// exclusion set is part of the kernel's contract, so it stays duplicated and
// is held together by an agreement test instead.
package canonicaljson

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Marshal reproduces Python's
//
//	json.dumps(value, sort_keys=True, separators=(",", ":"))
//
// byte for byte. This is the single most exacting function in the port: the
// plan's dispatch_fingerprint is a SHA-256 over exactly these bytes, so any
// difference at all -- a key sorted differently, a character escaped
// differently, a float formatted differently -- produces a different
// fingerprint and therefore a different plan.
//
// encoding/json cannot be used directly for this, for three reasons that are
// each individually sufficient:
//
//   - Python's json.dumps defaults to ensure_ascii=True, escaping every
//     non-ASCII rune as \uXXXX. Go emits raw UTF-8. "café" is 6 bytes in one
//     and 10 in the other.
//   - Go escapes <, > and & as <, > and & by default; Python
//     does not escape them at all. SetEscapeHTML(false) fixes that but not
//     the point above.
//   - Go sorts map keys but emits struct fields in declaration order, so the
//     ordering guarantee depends on which shape the value happens to have.
//
// So the plan is normalised through encoding/json into generic values first
// (maps, slices, float64, string, bool, nil) and then written out by this
// encoder, which owns every ordering and escaping decision explicitly.
func Marshal(value any) ([]byte, error) {
	normalized, err := Normalize(value)
	if err != nil {
		return nil, err
	}
	var builder strings.Builder
	if err := Write(&builder, normalized); err != nil {
		return nil, err
	}
	return []byte(builder.String()), nil
}

// Normalize rounds a value through encoding/json so structs,
// pointers and named types all arrive as the generic shapes writeCanonical
// understands. Doing this in one place means the encoder below never has to
// reflect over arbitrary types.
// Normalize is exported because the selector renders the same values in
// two other shapes (the indented wire plan, the spaced telemetry line) and
// must start from the identical normalised form.
func Normalize(value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var decoded any
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

// Write emits one value in canonical form. Exported for the same reason as
// Normalize: the selector's other renderers delegate scalars to it.
func Write(builder *strings.Builder, value any) error {
	switch typed := value.(type) {
	case nil:
		builder.WriteString("null")
	case bool:
		if typed {
			builder.WriteString("true")
			return nil
		}
		builder.WriteString("false")
	case string:
		writeString(builder, typed)
	case json.Number:
		// UseNumber keeps the literal the document carried, which is what
		// Python would have re-emitted for an int. A float that arrived as
		// 1.0 stays 1.0; an int that arrived as 1 stays 1.
		builder.WriteString(canonicalNumber(typed.String()))
	case float64:
		builder.WriteString(canonicalNumber(strconv.FormatFloat(typed, 'g', -1, 64)))
	case []any:
		builder.WriteByte('[')
		for index, element := range typed {
			if index > 0 {
				builder.WriteByte(',')
			}
			if err := Write(builder, element); err != nil {
				return err
			}
		}
		builder.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		// sort_keys=True. Python sorts by code point, which is what Go's
		// string comparison does for valid UTF-8.
		sort.Strings(keys)
		builder.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				builder.WriteByte(',')
			}
			writeString(builder, key)
			builder.WriteByte(':')
			if err := Write(builder, typed[key]); err != nil {
				return err
			}
		}
		builder.WriteByte('}')
	default:
		return fmt.Errorf("canonical json: unsupported type %T", value)
	}
	return nil
}

// canonicalNumber renders a JSON number the way Python's json would.
// An integral value carries no fractional part in either language, and
// json.Number preserves whatever the source wrote, so this is mostly a
// pass-through with one normalisation: Go's %g can emit exponent forms that
// Python would not for the small integers this plan contains.
func canonicalNumber(text string) string {
	if !strings.ContainsAny(text, "eE") {
		return text
	}
	parsed, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return text
	}
	if parsed == float64(int64(parsed)) {
		return strconv.FormatInt(int64(parsed), 10)
	}
	return text
}

// writeString escapes exactly as Python's json.dumps does with its
// default ensure_ascii=True.
//
// Python's escape table: `"` and `\` are backslash-escaped; \b \f \n \r \t
// have short forms; every other character below 0x20 becomes \u00XX; every
// character above 0x7e becomes \uXXXX, with astral planes written as a
// surrogate pair. Notably `<`, `>`, `&` and `/` are NOT escaped -- Go escapes
// the first three by default, which is why this cannot delegate.
func writeString(builder *strings.Builder, value string) {
	builder.WriteByte('"')
	for _, runeValue := range value {
		switch runeValue {
		case '"':
			builder.WriteString(`\"`)
			continue
		case '\\':
			builder.WriteString(`\\`)
			continue
		case '\b':
			builder.WriteString(`\b`)
			continue
		case '\f':
			builder.WriteString(`\f`)
			continue
		case '\n':
			builder.WriteString(`\n`)
			continue
		case '\r':
			builder.WriteString(`\r`)
			continue
		case '\t':
			builder.WriteString(`\t`)
			continue
		}
		switch {
		case runeValue < 0x20:
			fmt.Fprintf(builder, `\u%04x`, runeValue)
		case runeValue < 0x7f:
			builder.WriteRune(runeValue)
		case runeValue == utf8.RuneError:
			// An invalid byte decodes to RuneError; Python would have raised
			// on undecodable input long before this point, so emitting the
			// replacement character's escape is the closest faithful answer.
			builder.WriteString(`�`)
		case runeValue > 0xffff:
			high, low := utf16.EncodeRune(runeValue)
			fmt.Fprintf(builder, `\u%04x\u%04x`, high, low)
		default:
			fmt.Fprintf(builder, `\u%04x`, runeValue)
		}
	}
	builder.WriteByte('"')
}
