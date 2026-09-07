package canonicaljson_test

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/deagy/cadre/cli/internal/canonicaljson"
)

// TestWriteStringEscapesReplacementCharacter proves that the replacement
// character (U+FFFD) is escaped as � in the output, not written as a
// raw UTF-8 byte sequence. This ensures the output never contains raw non-ASCII
// bytes and maintains Python's json.dumps(ensure_ascii=True) compatibility.
func TestWriteStringEscapesReplacementCharacter(t *testing.T) {
	var builder strings.Builder
	// Construct a string containing the U+FFFD replacement character directly
	runeVal := rune(utf8.RuneError) // 0xFFFD = 65533
	testString := "before" + string(runeVal) + "after"

	canonicaljson.WriteString(&builder, testString)
	result := builder.String()

	// The output should be a quoted string with the replacement character escaped
	// as � (JSON escape sequence), not the raw UTF-8 bytes
	expected := "\"before\\ufffdafter\""
	if result != expected {
		t.Errorf("WriteString with U+FFFD replacement char: got %q, expected %q", result, expected)
	}

	// Verify the result contains only ASCII bytes (all non-ASCII should be escaped)
	if !isValidASCII(result) {
		t.Errorf("output contains non-ASCII bytes: %q", result)
	}
}

// TestMarshalPreservesReplacementCharacterEscape proves that when a value
// containing U+FFFD is marshaled, the output contains the escape sequence
// and can be unmarshaled back.
func TestMarshalPreservesReplacementCharacterEscape(t *testing.T) {
	testString := "café" + string(rune(utf8.RuneError)) + "end"
	value := map[string]any{"text": testString}

	result, err := canonicaljson.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Verify result is valid UTF-8
	if !utf8.Valid(result) {
		t.Errorf("result is not valid UTF-8: %q", result)
	}

	// Verify result contains only ASCII (all non-ASCII should be escaped)
	resultStr := string(result)
	if !isValidASCII(resultStr) {
		t.Logf("DEBUG: result string %%q: %q", resultStr)
		// Print non-ASCII bytes
		for i := 0; i < len(resultStr); i++ {
			if resultStr[i] > 0x7f {
				t.Logf("DEBUG: byte[%d] = 0x%02x >0x7f", i, resultStr[i])
			}
		}
		t.Errorf("result contains non-ASCII bytes; expected ensure_ascii=True behavior")
	}

	// Verify the result contains the escaped form (as JSON escape sequence).
	// The escape sequence for U+FFFD should be present in lowercase hex (\\ufffd)
	if !strings.Contains(resultStr, "\\ufffd") {
		t.Errorf("result does not contain escaped replacement char \\ufffd; got: %s", resultStr)
	}

	// Verify the output is JSON that can be unmarshaled back
	var unmarshaled map[string]any
	if err := json.Unmarshal(result, &unmarshaled); err != nil {
		t.Fatalf("unmarshaling result: %v", err)
	}

	// The unmarshaled text should contain the U+FFFD replacement character
	if recovered, ok := unmarshaled["text"].(string); ok {
		if !strings.Contains(recovered, string(rune(utf8.RuneError))) {
			t.Errorf("unmarshaled text does not contain U+FFFD")
		}
	} else {
		t.Fatal("unmarshaled text is not a string")
	}
}

// isValidASCII returns true if the string contains only ASCII bytes (0x00-0x7F).
func isValidASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 0x7F {
			return false
		}
	}
	return true
}
