// Package githubapproval turns a GitHub PR review into the Approval shape the
// graph's human-approval interrupt expects, so a real review can stand in for
// a manually typed decision.
//
// Ported from engine/agentic_sdlc_langgraph/github_approval.py.
//
// The graph-resume entry point is not here: it is three lines around
// graph.invoke(Command(resume=approval)) and needs the executor, which does
// not exist yet. Everything that decides *whether* a review is an approval --
// which is the part that matters -- is here.
package githubapproval

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/deagy/cadre/cli/internal/engine/provider"
	"github.com/deagy/cadre/cli/internal/engine/state"
)

// ReviewsMockEnvVar is the mocking convention the legacy suite uses, kept so
// tests need neither the network nor a gh binary.
const ReviewsMockEnvVar = "AGENTIC_SDLC_TEST_GITHUB_REVIEWS_FILE"

var reviewURIPattern = regexp.MustCompile(
	`^github-review:([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+):pull/([0-9]+):review/([0-9]+):reviewer/([A-Za-z0-9-]+)$`)

var commitSHAPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Review is one GitHub PR review, as much of it as this adapter reads.
type Review struct {
	ID             any            `json:"id"`
	User           map[string]any `json:"user"`
	SubmittedAt    string         `json:"submitted_at"`
	State          string         `json:"state"`
	CommitID       any            `json:"commit_id"`
	DismissedState string         `json:"dismissed_state"`
}

// Login returns the reviewer's login, or empty when absent.
func (r Review) Login() string {
	login, _ := r.User["login"].(string)
	return login
}

// ParseReviewURI parses a github-review: URI, returning nil when it does not match.
func ParseReviewURI(value string) []string {
	return reviewURIPattern.FindStringSubmatch(value)
}

// NormalizeCommitSHA lowercases a full 40-hex sha, or returns empty.
func NormalizeCommitSHA(value any) string {
	text, isText := value.(string)
	if !isText {
		return ""
	}
	lowered := strings.ToLower(strings.TrimSpace(text))
	if !commitSHAPattern.MatchString(lowered) {
		return ""
	}
	return lowered
}

// isValidDateTime requires a timestamp carrying an offset.
func isValidDateTime(value string) bool {
	if value == "" {
		return false
	}
	normalised := value
	if !strings.Contains(normalised, "T") {
		normalised = strings.Replace(normalised, " ", "T", 1)
	}
	_, err := time.Parse(time.RFC3339, normalised)
	return err == nil
}

func parseTime(value string) (time.Time, bool) {
	normalised := value
	if !strings.Contains(normalised, "T") {
		normalised = strings.Replace(normalised, " ", "T", 1)
	}
	parsed, err := time.Parse(time.RFC3339, normalised)
	return parsed, err == nil
}

// FetchReviews reads a PR's reviews, honouring the mock env var when set.
func FetchReviews(repo string, pr int) ([]Review, error) {
	path := os.Getenv(ReviewsMockEnvVar)
	if path == "" {
		return nil, fmt.Errorf("fetching GitHub reviews requires %s to be set in this build", ReviewsMockEnvVar)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var reviews []Review
	if err := json.Unmarshal(contents, &reviews); err != nil {
		return nil, fmt.Errorf("GitHub reviews response must be a JSON array: %w", err)
	}
	return reviews, nil
}

// SelectReview returns the latest effective approval from a reviewer.
//
// Latest wins: reviews from the reviewer with a valid timestamp (and matching
// commit, when one is given) are ordered, and only the last one counts. A
// later CHANGES_REQUESTED therefore invalidates an earlier APPROVED, which is
// the entire point of the rule.
//
// Ordering is chronological, by parsed instant. The Python sorted the
// timestamps as *strings*, which agrees with chronology only while every
// timestamp shares an offset. GitHub returns UTC, so the defect was latent --
// but with mixed offsets it inverts: "2026-08-18T12:00:00+02:00" sorts after
// "2026-08-18T11:00:00Z" while being an hour earlier, so a stale APPROVED
// wins over the CHANGES_REQUESTED that superseded it. Verified against the
// Python before changing it.
func SelectReview(reviews []Review, reviewerLogin, commitSHA string) (Review, error) {
	normalisedLogin := strings.ToLower(reviewerLogin)
	normalisedCommit := NormalizeCommitSHA(commitSHA)

	type candidate struct {
		review Review
		at     time.Time
	}
	var matching []candidate

	for _, review := range reviews {
		login := review.Login()
		if login == "" || !strings.EqualFold(login, normalisedLogin) {
			continue
		}
		at, parsed := parseTime(review.SubmittedAt)
		if !parsed || !isValidDateTime(review.SubmittedAt) {
			continue
		}
		if normalisedCommit != "" && NormalizeCommitSHA(review.CommitID) != normalisedCommit {
			continue
		}
		matching = append(matching, candidate{review, at})
	}

	if len(matching) == 0 {
		commitText := ""
		if commitSHA != "" {
			commitText = " at commit " + commitSHA
		}
		return Review{}, fmt.Errorf("no GitHub review found for reviewer %s%s", reviewerLogin, commitText)
	}

	sort.SliceStable(matching, func(i, j int) bool { return matching[i].at.Before(matching[j].at) })
	latest := matching[len(matching)-1].review

	dismissed := strings.EqualFold(latest.DismissedState, "dismissed")
	if latest.State != "APPROVED" || dismissed {
		return Review{}, fmt.Errorf("latest GitHub review for reviewer %s is not an effective approval", reviewerLogin)
	}
	return latest, nil
}

// ApprovalOptions carries the context needed to build an Approval.
type ApprovalOptions struct {
	GateID        string
	AuthorityID   string
	RoleLabel     string
	Repo          string
	PR            int
	ExpectedLogin string // optional; skipped when empty
	DecidedAt     string // optional; now() when empty
}

// ReviewToApproval builds an Approval from an already-selected review.
//
// It reads no project overlay: the legacy version looked up an authority's
// assigned human in authorities.json, and without that overlay the supplied
// authority id is the identity recorded. ExpectedLogin is the one piece of
// that cross-check that survives -- when given, the review's author must
// match it.
func ReviewToApproval(review Review, options ApprovalOptions) (state.Approval, error) {
	var approval state.Approval

	login := review.Login()
	if login == "" {
		return approval, fmt.Errorf("review is missing a reviewer login")
	}
	if options.ExpectedLogin != "" && !strings.EqualFold(login, options.ExpectedLogin) {
		return approval, fmt.Errorf("GitHub reviewer %s does not match expected authority login %s",
			login, options.ExpectedLogin)
	}
	if review.ID == nil {
		return approval, fmt.Errorf("review is missing an id")
	}
	if !isValidDateTime(review.SubmittedAt) {
		return approval, fmt.Errorf("review submitted_at %q is not a valid date-time", review.SubmittedAt)
	}

	reviewID := renderID(review.ID)
	normalisedLogin := strings.ToLower(login)
	reviewURI := fmt.Sprintf("github-review:%s:pull/%d:review/%s:reviewer/%s",
		options.Repo, options.PR, reviewID, normalisedLogin)
	if ParseReviewURI(reviewURI) == nil {
		return approval, fmt.Errorf("invalid GitHub review URI components for %s", reviewURI)
	}

	decidedAt := options.DecidedAt
	if decidedAt == "" {
		decidedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}

	evidencePayload := map[string]any{
		"gate_id":        options.GateID,
		"authority_id":   options.AuthorityID,
		"repo":           options.Repo,
		"pull":           options.PR,
		"review_id":      review.ID,
		"reviewer_login": login,
		"decided_at":     decidedAt,
		"commit_sha":     NormalizeCommitSHA(review.CommitID),
	}
	digest, err := provider.Fingerprint(evidencePayload)
	if err != nil {
		return approval, err
	}

	return state.Approval{
		Status:    "approved",
		Approver:  &state.Identity{ID: options.AuthorityID, Role: options.RoleLabel, Kind: "human"},
		DecidedAt: &decidedAt,
		EvidenceRefs: []state.Evidence{{
			EvidenceID:     fmt.Sprintf("%s-%s-github-review-%s", strings.ToLower(options.GateID), options.AuthorityID, reviewID),
			URI:            reviewURI,
			HashAlgorithm:  "sha256",
			Hash:           strings.TrimPrefix(digest, "sha256:"),
			Classification: "internal",
		}},
	}, nil
}

// renderID formats a review id the way JSON decoding leaves it: GitHub sends a
// number, which arrives as float64 and must not render as "1.234e+06".
func renderID(value any) string {
	switch typed := value.(type) {
	case float64:
		return fmt.Sprintf("%d", int64(typed))
	case string:
		return typed
	default:
		return fmt.Sprintf("%v", typed)
	}
}
