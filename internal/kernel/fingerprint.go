package kernel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/deagy/cadre/cli/internal/canonicaljson"
)

// The kernel's side of the dispatch fingerprint.
//
// `validate` recomputes a plan's fingerprint from the plan's own content and
// compares it to the one stored in the file. That is what makes a dispatch
// plan tamper-evident: editing the plan after the fact -- adding a reviewer,
// removing a gate -- changes what it hashes to, and the stored value no
// longer matches.
//
// The check only works while this agrees with the selector exactly. It did
// not: the selector excluded `provenance` from the hashed payload and the
// kernel did not, so every plan `cadre select` produced was rejected by the
// kernel that read it. The two exclusion sets are duplicated deliberately --
// each is part of its own side's contract -- and held together by an
// agreement test rather than by a shared constant, so that a change to one
// side fails loudly instead of silently re-opening the same gap.

// FingerprintExcludedKeys are the keys omitted from the hashed payload.
//
// generated_at is wall-clock; provenance describes the environment the plan
// was generated in (working-tree state, which files were passed) rather than
// anything the selector decided. Hashing either would make the fingerprint a
// checksum over noise, and re-running an identical selection would produce a
// plan that failed its own integrity check.
var FingerprintExcludedKeys = map[string]bool{
	"generated_at": true, "dispatch_fingerprint": true, "provenance": true,
}

// Fingerprint hashes a value in canonical form.
func Fingerprint(value any) (string, error) {
	canonical, err := canonicaljson.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("fingerprint: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// DispatchFingerprint binds every decision-relevant field of a dispatch plan.
func DispatchFingerprint(dispatch map[string]any) (string, error) {
	payload := make(map[string]any, len(dispatch))
	for key, value := range dispatch {
		if FingerprintExcludedKeys[key] {
			continue
		}
		payload[key] = value
	}
	return Fingerprint(payload)
}

// decodeJSONObject parses bytes that must be a JSON object.
func decodeJSONObject(data []byte) (map[string]any, error) {
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, fmt.Errorf("expected a JSON object, got null")
	}
	return object, nil
}
