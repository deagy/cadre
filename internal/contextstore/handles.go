// handles.go ports handles.py: mint, validate, and nothing else.
//
// A handle is `ctx_` followed by 32 lowercase hex characters (128 bits from
// a CSPRNG). Random, deliberately not content-derived -- content addressing
// would give free deduplication, but identical content stored by two
// different agents in two different scopes would then collide onto one
// handle, turning the store into an equality oracle across exactly the
// boundary the scope model exists to hold.
package contextstore

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
)

const (
	HandlePrefix    = "ctx_"
	HandleHexLength = 32
	handleHexBytes  = HandleHexLength / 2
)

var handlePattern = regexp.MustCompile(`^` + HandlePrefix + `[0-9a-f]{32}$`)

// MintHandle generates a new random handle.
func MintHandle() (string, error) {
	buf := make([]byte, handleHexBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return HandlePrefix + hex.EncodeToString(buf), nil
}

// IsHandle reports whether value is a well-formed handle.
func IsHandle(value string) bool {
	return handlePattern.MatchString(value)
}

// ValidateHandle returns value if it is well-formed, else an error naming
// the expected shape -- validated before it reaches a query so a malformed
// handle fails clearly, rather than silently matching nothing and being
// indistinguishable from an expired entry.
func ValidateHandle(value string) (string, error) {
	if !IsHandle(value) {
		return "", fmt.Errorf(
			"malformed handle: expected '%s' followed by %d lowercase hex characters, got %q",
			HandlePrefix, HandleHexLength, value)
	}
	return value, nil
}
