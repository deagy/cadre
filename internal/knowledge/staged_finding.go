package knowledge

// Build a staged record from a structured finding, generating the ceremony
// fields.
//
// The staged-record contract requires thirteen frontmatter keys, three of
// which are pure bookkeeping that a human or an agent should never have to
// hand-compute: `id` (must match the KS-YYYYMMDD-slug pattern and be unique),
// `content_digest` (a sha256 of the body under a specific normalisation), and
// `status` (always "proposed" for a fresh submission). Every other required
// field is a judgement call about the finding itself, and this file
// deliberately does not touch those.
//
// What this does NOT do: it does not verify that a finding is genuine, that an
// agent actually observed what it claims, or that the fields supplied are
// honest. Nothing here can verify that a review produced a finding during a
// review, only that the finding it claims to have produced is well-formed once
// handed over. Do not read a record built here, or a passing `--render-only`
// call, as evidence that a review happened.
//
// It also does not default `untrusted_instruction_risk` or
// `proposed_classification`. Both are required with no fallback: the
// automatic-defer rule exists because that risk assessment cannot be inferred,
// and a classification default would be a permissiveness judgement this code
// has no basis to make on the caller's behalf.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// stagedSlugMaxLength caps the slug portion of a generated id: long enough to
// stay recognisable from the title, short enough that an id stays a single
// reasonable token in logs and CLI arguments.
const stagedSlugMaxLength = 40

// stagedDigestSuffixLength is how many hex characters of the content digest
// are folded into the id.
//
// This is what makes two different findings that happen to slugify to the same
// title on the same day resolve to different ids in practice, without making
// the id a second, competing digest of the body -- it borrows the one
// ComputeStagedDigest already produces rather than computing anything new.
// PutGeneratedStagedRecord is still the actual safety net; this only keeps the
// common case from colliding at all.
const stagedDigestSuffixLength = 12

var stagedSlugInvalid = regexp.MustCompile(`[^a-z0-9]+`)

// FindingError means a structured finding is missing a field or is not
// well-formed input.
type FindingError struct {
	Message string
}

func (e *FindingError) Error() string { return e.Message }

// StagedFindingKeys are the frontmatter fields a finding must supply: the
// required-key set minus the two that are generated (`id`, `content_digest`)
// and the one that is always the same for a fresh proposal (`status`, always
// "proposed"). Derived from StagedRequiredKeys rather than hand-written a
// second time, so a change to the contract cannot silently leave this behind.
func StagedFindingKeys() []string {
	generated := []string{"id", "content_digest", "status"}
	keys := make([]string, 0, len(StagedRequiredKeys))
	for _, key := range StagedRequiredKeys {
		if !containsString(generated, key) {
			keys = append(keys, key)
		}
	}
	return keys
}

func stagedSlugify(title string) string {
	slug := stagedSlugInvalid.ReplaceAllString(strings.ToLower(strings.TrimSpace(title)), "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		return "finding"
	}
	if len(slug) > stagedSlugMaxLength {
		slug = slug[:stagedSlugMaxLength]
	}
	slug = strings.Trim(slug, "-")
	if slug == "" {
		return "finding"
	}
	return slug
}

// GenerateStagedID returns a deterministic id for (title, digest) on a given
// UTC date.
//
// Deterministic given the same inputs: the same title, on the same UTC date,
// over content that hashes to the same digest, always produces the same id --
// proposing the identical finding twice is idempotent by construction rather
// than by accident. Different content almost always produces a different id
// even when the title is reused verbatim. "Almost always" is not "always" -- a
// genuine collision must fail loudly rather than silently overwrite, and that
// check lives in PutGeneratedStagedRecord, because only the storage layer
// knows what (if anything) already occupies the id.
func GenerateStagedID(title, digest string, when time.Time) string {
	suffix := digest
	if len(suffix) > stagedDigestSuffixLength {
		suffix = suffix[:stagedDigestSuffixLength]
	}
	return fmt.Sprintf("KS-%s-%s-%s", when.UTC().Format("20060102"), stagedSlugify(title), suffix)
}

// BuildStagedRecordFromFinding turns a structured finding into (frontmatter,
// body), ready for validation.
//
// `finding` must carry a `summary` key plus every key in StagedFindingKeys.
// `summary` becomes the record's Markdown body -- that name, not `body`,
// because it is the name the dispatch contract tells agents to return, and a
// finding an agent produced by following that contract must be stageable
// without a translation step nobody defined. No field is defaulted: a missing
// key is reported by name rather than silently filled in.
//
// The caller must still route the result through PutStagedRecord or
// PutGeneratedStagedRecord (which validate) before treating it as a valid
// record. The validation run here is a fail-fast convenience, not a substitute
// for the write-time check.
func BuildStagedRecordFromFinding(finding map[string]any, when time.Time) (map[string]any, string, error) {
	var missing []string
	for _, key := range StagedFindingKeys() {
		if _, present := finding[key]; !present {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, "", &FindingError{Message: fmt.Sprintf(
			"finding is missing required field(s): %s. Every field the frontmatter contract requires must "+
				"be supplied explicitly -- this generates only 'id', 'content_digest', and 'status', never "+
				"a judgement call that belongs to whoever is proposing the finding.",
			strings.Join(missing, ", "))}
	}
	body, ok := finding["summary"].(string)
	if !ok || strings.TrimSpace(body) == "" {
		return nil, "", &FindingError{Message: "finding['summary'] must be a non-empty string: it becomes " +
			"the record's markdown body, everything the reader needs beyond the frontmatter's structured " +
			"fields. 'summary' is the name the dispatch contract tells agents to return " +
			"(.agents/skills/run-agent-orchestration/references/dispatch-contract.md)"}
	}

	frontmatter := map[string]any{"status": "proposed"}
	for _, key := range StagedFindingKeys() {
		frontmatter[key] = finding[key]
	}
	digest := ComputeStagedDigest(body)
	frontmatter["content_digest"] = digest
	// A malformed (non-string) title is reported by the validator below, not
	// treated as a crash here: GenerateStagedID only needs something to
	// slugify, and stagedSlugify already tolerates an empty string.
	title, _ := frontmatter["title"].(string)
	frontmatter["id"] = GenerateStagedID(title, digest, when)

	if findings := ValidateStagedRecord(frontmatter, body); len(findings) > 0 {
		return nil, "", &FindingError{Message: "the generated record does not satisfy the staged-record " +
			"contract: " + strings.Join(findings, "; ")}
	}
	return frontmatter, body, nil
}
