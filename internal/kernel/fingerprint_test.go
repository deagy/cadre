package kernel

import (
	"encoding/json"
	"strings"
	"testing"
)

// The kernel's own fingerprint, checked against the bytes Python would hash.
//
// The agreement test in internal/canonicaljson compares this side with the
// selector's. This one compares it with Python, and specifically over the
// input where a Go-native encoder silently differs: json.dumps defaults to
// ensure_ascii=True, so "café" is six bytes on one side and ten on the other.
// A digest computed over the shorter form is a valid sha256 of the wrong
// thing, which is the kind of disagreement nothing reports.

func TestFingerprintHashesTheBytesPythonWouldHash(t *testing.T) {
	for _, probe := range []struct {
		name     string
		value    any
		expected string // sha256 of json.dumps(value, sort_keys=True, separators=(",", ":"))
	}{
		{
			// json.dumps({"s": "café"}, sort_keys=True, separators=(",",":"))
			// is {"s":"caf\u00e9"} -- ten bytes for the string, not six.
			"non-ASCII is escaped before hashing",
			map[string]any{"s": "café"},
			`{"s":"caf\u00e9"}`,
		},
		{
			// Python escapes neither < nor & ; Go's encoder escapes both by
			// default, which is the other half of the same problem.
			"HTML punctuation is not escaped",
			map[string]any{"s": "a<b>c&d"},
			`{"s":"a<b>c&d"}`,
		},
		{
			"keys are sorted and separators are compact",
			map[string]any{"zebra": 1, "alpha": 2},
			`{"alpha":2,"zebra":1}`,
		},
		{
			"astral runes become surrogate pairs",
			map[string]any{"s": "🚀"},
			`{"s":"\ud83d\ude80"}`,
		},
	} {
		t.Run(probe.name, func(t *testing.T) {
			digest, err := Fingerprint(probe.value)
			if err != nil {
				t.Fatal(err)
			}
			expected := "sha256:" + hexSHA256([]byte(probe.expected))
			if digest != expected {
				t.Errorf("Fingerprint = %s, want %s (over %s)", digest, expected, probe.expected)
			}
		})
	}
}

func TestProviderDigestsUseTheSameEncoder(t *testing.T) {
	// provider.go kept its own encoder until this test existed. It agreed for
	// ASCII and disagreed for everything else, so provider digests over a
	// manifest containing one accented character would have differed between
	// the two kernels -- and the difference reads as "the provider changed".
	value := map[string]any{"description": "Fournisseur sécurisé", "id": "café-provider"}

	viaProvider, err := fingerprint(value)
	if err != nil {
		t.Fatal(err)
	}
	viaKernel, err := Fingerprint(value)
	if err != nil {
		t.Fatal(err)
	}
	if viaProvider != viaKernel {
		t.Errorf("provider fingerprint %s != kernel fingerprint %s", viaProvider, viaKernel)
	}

	// And neither is the Go-native answer, or the comparison above would hold
	// with both sides wrong.
	native, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if viaKernel == "sha256:"+hexSHA256(native) {
		t.Error("the digest matches Go's own encoding, which Python would not produce")
	}
	if !strings.Contains(string(native), "é") {
		t.Error("the probe value lost its non-ASCII content; this test would prove nothing")
	}
}
