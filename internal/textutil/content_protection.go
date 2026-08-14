// Package textutil holds secret redaction, text chunking, and offline
// embedding/similarity utilities shared by both the knowledge store
// (internal/knowledge) and the context store, ports of
// roster/shared/src/content_protection.py, text_chunking.py, and
// text_embedding.py.
//
// This package's existence mirrors roster/shared/src/'s own role in the
// Python original: the knowledge store and context store must not import
// each other (roster/orchestration/test/test_context_boundary.py enforces
// this on the Python side, since "no path exists from working context into
// the curated corpus without a steward disposition" needs to be a property
// of the import graph, not a promise in a document) -- so a utility both
// need lives in a directory neither owns. internal/knowledge must never
// import this package's future context-store sibling, and vice versa;
// both may depend on textutil.
//
// content_protection.go deliberately omits the openai-compatible embedding
// provider (which stays knowledge-store-only in the Python original, per
// OD-5 in that module's docstring: whether context-store material may be
// transmitted to a third-party embedding endpoint is a deliberately
// unresolved, refused-for-now security decision, enforced by keeping that
// code out of reach rather than behind a config flag). Only the offline,
// deterministic hashing embedding in embedding.go is shared.
package textutil

import "regexp"

// secretPattern is one labeled secret-detection pattern.
type secretPattern struct {
	label   string
	pattern *regexp.Regexp
}

// SecretPatterns are the redaction patterns applied by ProtectContent.
var SecretPatterns = []secretPattern{
	{"private-key", regexp.MustCompile(`(?s)-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----.*?-----END (?:RSA |EC |OPENSSH )?PRIVATE KEY-----`)},
	{"bearer-token", regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/\-]+=*`)},
	{"aws-access-key", regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`)},
	{"github-token", regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{30,}\b`)},
	{"generic-secret", regexp.MustCompile(`(?i)\b(api[_-]?key|secret|password|token)\s*[:=]\s*["']?[^\s,"']{8,}["']?`)},
}

// InjectionPatterns flag likely prompt-injection content in ProtectContent's
// InjectionRisk field.
var InjectionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)ignore (?:all |any )?(?:previous|prior|above) instructions`),
	regexp.MustCompile(`(?i)reveal (?:the )?(?:system|developer) prompt`),
	regexp.MustCompile(`(?i)act as (?:the )?system`),
	regexp.MustCompile(`(?i)bypass (?:security|policy|approval|guardrail)`),
	regexp.MustCompile(`(?i)do not tell (?:the )?user`),
}

// ProtectedContent is ProtectContent's result.
type ProtectedContent struct {
	Content       string
	Redactions    []string
	InjectionRisk bool
}

// ProtectContent redacts secret-shaped substrings from content (when
// enabled) and reports whether the (possibly redacted) result still
// matches a likely prompt-injection pattern.
func ProtectContent(content string, enabled bool) ProtectedContent {
	protected := content
	var redactions []string
	if enabled {
		for _, sp := range SecretPatterns {
			label := sp.label
			protected = sp.pattern.ReplaceAllStringFunc(protected, func(string) string {
				redactions = append(redactions, label)
				return "[REDACTED:" + label + "]"
			})
		}
	}
	injectionRisk := false
	for _, p := range InjectionPatterns {
		if p.MatchString(protected) {
			injectionRisk = true
			break
		}
	}
	return ProtectedContent{Content: protected, Redactions: redactions, InjectionRisk: injectionRisk}
}
