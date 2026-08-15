package selector

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/deagy/cadre/cli/internal/canonicaljson"
)

// CanonicalJSON writes the bytes a dispatch fingerprint is taken over.
//
// The encoder itself lives in internal/canonicaljson, shared with the kernel:
// the kernel recomputes this fingerprint during validate, and if the two
// encoders disagree by a single byte it rejects every plan the selector
// produced. See that package's doc comment.
func CanonicalJSON(value any) ([]byte, error) { return canonicaljson.Marshal(value) }

// normalizeForCanonical and writeCanonical are what the plan and telemetry
// renderers build on: both start from the same normalised value and delegate
// scalars to the same writer, differing only in indentation, separators, and
// whether non-ASCII is escaped.
func normalizeForCanonical(value any) (any, error) { return canonicaljson.Normalize(value) }

func writeCanonical(builder *strings.Builder, value any) error {
	return canonicaljson.Write(builder, value)
}

// FingerprintExcludedKeys are the plan keys omitted from the hashed payload.
//
// generated_at is wall-clock and provenance varies by generation-time
// environment (working-tree dirty state, which files were passed in), so
// including either would make dispatch_fingerprint a checksum over
// environment noise instead of a determinism check over what the selector
// actually decided.
var FingerprintExcludedKeys = []string{"generated_at", "dispatch_fingerprint", "provenance"}

// DispatchFingerprint computes a plan's fingerprint over its canonical form.
func DispatchFingerprint(plan map[string]any) (string, error) {
	payload := make(map[string]any, len(plan))
	excluded := setOf(FingerprintExcludedKeys)
	for key, value := range plan {
		if excluded[key] {
			continue
		}
		payload[key] = value
	}
	canonical, err := CanonicalJSON(payload)
	if err != nil {
		return "", err
	}
	return "sha256:" + sha256Hex(canonical), nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
