package kernel

import (
	"fmt"
	"regexp"
	"strings"
)

// `publish-gate-status` -- render a task's gate state onto its pull request or
// merge request, and update that same comment in place on every re-run.
//
// **Strictly one-way.** This comment is diagnostics for humans watching a
// review, and nothing else. Nothing in this kernel ever reads it, its
// reactions, or its replies back into gate state -- there is no
// approve-from-a-comment adapter and there will not be one. The rendered body
// says so in as many words, so a reader cannot mistake it for a sign-off
// surface, and this module never touches a reactions endpoint.
//
// **No free-text surface at all.** Every rendered token is fixed template
// text, a closed-enum value, a bundled contract's gate name, a hex hash, a
// timestamp, or a small integer. That is why there is no sanitizer here and
// must never be: the moment something project-supplied is rendered, this
// module needs the machinery gate_issues has, and adding the machinery without
// noticing the change is how that happens quietly.
//
// The re-entry history is reduced to a count and the earliest re-entered gate.
// The actor and the reason -- a real identity and free text -- are never
// rendered, and the classification is used only for the --allow-classification
// check.

const (
	statusTemplateVersion = 1
	// The pagination cap. Ten pages of a hundred is a thousand comments, and
	// beyond that this module refuses rather than guessing -- see
	// listStatusComments.
	maxCommentPages = 10
	commentPageSize = 100
)

// The two forges this can render onto.
const (
	ForgeGitHub = "github"
	ForgeGitLab = "gitlab"
)

// advisoryParagraph is the part of the comment that exists to be read by
// somebody about to make a mistake.
const advisoryParagraph = "**This comment is not an approval and is never read back.**\n" +
	"Approving this merge request, reacting to this comment, replying \"LGTM\", or\n" +
	"closing anything linked from it does not approve any lifecycle gate.\n" +
	"`agentic-sdlc` never reads this comment, its reactions, or its replies back\n" +
	"into gate state — this render is strictly one-way. Gate approval is recorded\n" +
	"only by `agentic-sdlc decide` or `agentic-sdlc approve-from-gitlab-mr` /\n" +
	"`approve-from-github-pr`, against an external approval record. If anyone cites\n" +
	"this comment as evidence that a gate is approved, they are mistaken."

// GateStatusError is a structural or policy failure -- exit 1.
type GateStatusError struct{ Message string }

func (e *GateStatusError) Error() string { return e.Message }

// GateStatusBlocked needs a human -- exit 2.
//
// Separate from an error because the difference is actionable: an ambiguous
// match, a comment somebody else wrote, more comments than this can page
// through, a held lock, or a write that did not verify. Each means "do not
// proceed until a person looks", not "this is broken".
type GateStatusBlocked struct{ Message string }

func (e *GateStatusBlocked) Error() string { return e.Message }

// ComputeStatusMarker is the token that identifies this comment.
//
// Domain-separated from the issue markers by a NUL-prefixed label, so the same
// task id cannot produce the same marker for two different kinds of artifact.
func ComputeStatusMarker(taskID string) string {
	return hexSHA256([]byte("gate-status\x00" + taskID))[:16]
}

// markerPattern matches this task's comment at any template version.
//
// Any version on purpose: a future v2 template must still find and update a v1
// comment rather than posting a second one beside it.
func markerPattern(marker string) *regexp.Regexp {
	return regexp.MustCompile(
		`<!-- agentic-sdlc:gate-status:v\d+:` + regexp.QuoteMeta(marker) + ` -->`)
}

// renderedAtPattern finds the live timestamp in a rendered body.
var renderedAtPattern = regexp.MustCompile(`(· rendered ).*`)

// canonicaliseForComparison blanks the timestamp before comparing bodies.
//
// The timestamp changes on every invocation by design, so comparing it would
// make every run report a change and rewrite a comment that says exactly what
// it said before. The body actually posted still carries the real one; only
// the comparison is normalised.
func canonicaliseForComparison(body string) string {
	return renderedAtPattern.ReplaceAllString(body, "${1}<omitted-for-comparison>")
}

// earliestReenteredGate is the earliest gate any re-entry started from.
func earliestReenteredGate(history []any) any {
	earliest := -1
	for _, raw := range history {
		// Either shape: the projection carries ordered objects because it was
		// decoded order-preserving, and a caller constructing one by hand
		// passes plain maps. Handling only one silently skips every entry.
		gateID, _ := fieldOf(raw, "earliest_gate").(string)
		if gateID == "" {
			continue
		}
		index := gateIndex(gateID)
		if index >= len(GateIDs) {
			continue
		}
		if earliest == -1 || index < earliest {
			earliest = index
		}
	}
	if earliest == -1 {
		return nil
	}
	return GateIDs[earliest]
}

// statusCell is what one gate's row says.
func statusCell(gate map[string]any, humanOnly bool) string {
	if gate["applicability"] == "not-applicable" {
		return "not applicable"
	}
	if reentry, present := gate["required_reentry_gate"]; present && reentry != nil {
		return fmt.Sprintf("invalidated (re-entry required from %v)", reentry)
	}
	status := fmt.Sprint(gate["status"])
	// Marked so a reader does not wait for automation to move it. A human-only
	// gate that is not approved is waiting on a person, and nothing this
	// kernel does will advance it.
	if humanOnly && status != "approved" {
		return status + " (human-only gate)"
	}
	return status
}

// RenderGateStatusBody builds the comment.
//
// Pure, and takes its content from exactly two places: the run record's
// read-only projection, and the bundled lifecycle contract. Nothing else is in
// scope, which is what keeps the no-free-text property true rather than
// aspirational.
func RenderGateStatusBody(
	taskID string, projection *orderedObject,
	contracts map[string]map[string]any, renderedAt string,
) string {
	marker := ComputeStatusMarker(taskID)

	gateByID := map[string]map[string]any{}
	for _, raw := range listOf(projection.values["gates"]) {
		switch gate := raw.(type) {
		case *orderedObject:
			id, _ := gate.values["gate_id"].(string)
			gateByID[id] = gate.values
		case map[string]any:
			id, _ := gate["gate_id"].(string)
			gateByID[id] = gate
		}
	}

	lines := []string{
		fmt.Sprintf("<!-- agentic-sdlc:gate-status:v%d:%s -->", statusTemplateVersion, marker),
		"> Machine-generated by agentic-sdlc. Not a human-authored artifact. **Not approval evidence.**",
		"> Reacting or replying to this comment does not approve anything.",
		"",
		// A different hash from the marker, deliberately: this one is for a
		// human to recognise, that one is for matching. Conflating them would
		// put the matching token in the visible text, where an editor could
		// break it.
		fmt.Sprintf("**Lifecycle gate status — task `%s`**", TaskHash(taskID)),
		fmt.Sprintf("Current phase: %v · rendered %s",
			projection.values["current_phase"], renderedAt),
		"",
		"| Gate | Status |",
		"| --- | --- |",
	}
	for _, gateID := range GateIDs {
		gate := gateByID[gateID]
		contract := contracts[gateID]
		gateName := gateID
		if name, ok := contract["name"].(string); ok && name != "" {
			gateName = name
		}
		lines = append(lines, fmt.Sprintf("| %s %s | %s |",
			gateID, gateName, statusCell(gate, contract["human_only"] == true)))
	}
	lines = append(lines, "")

	history := listOf(projection.values["re_entry_history"])
	if len(history) > 0 {
		// A count and the earliest gate, and nothing else. The actor and the
		// reason are a real identity and free text; neither belongs on a
		// forge comment this kernel renders automatically.
		lines = append(lines, fmt.Sprintf(
			"Re-entries recorded: %d (earliest re-entered gate: %v)",
			len(history), earliestReenteredGate(history)))
		lines = append(lines, "")
	}

	lines = append(lines, "---", "")
	lines = append(lines, strings.Split(advisoryParagraph, "\n")...)
	return strings.Join(lines, "\n") + "\n"
}

// NormalizedComment is the only shape a forge comment is ever read into.
//
// Four fields. Never reactions, never award emoji, never anything else -- a
// field this type does not carry is a field no code above it can accidentally
// start depending on.
type NormalizedComment struct {
	ID       any
	Body     string
	Author   string
	IsSystem bool
}

func normaliseGitHubComment(raw map[string]any) NormalizedComment {
	author := ""
	if user, ok := raw["user"].(map[string]any); ok {
		author, _ = user["login"].(string)
	}
	body, _ := raw["body"].(string)
	return NormalizedComment{ID: raw["id"], Body: body, Author: author}
}

func normaliseGitLabNote(raw map[string]any) NormalizedComment {
	author := ""
	if user, ok := raw["author"].(map[string]any); ok {
		author, _ = user["username"].(string)
	}
	body, _ := raw["body"].(string)
	return NormalizedComment{
		ID: raw["id"], Body: body, Author: author, IsSystem: raw["system"] == true,
	}
}

// statusAdapter is what the two forges have in common: verify who we are, read
// the comments, write one.
type statusAdapter interface {
	VerifyIdentity(expected string) (string, error)
	ListComments() ([]NormalizedComment, error)
	CreateComment(body string) (NormalizedComment, error)
	UpdateComment(commentID any, body string) (NormalizedComment, error)
	Mocked() bool
	Target() *orderedObject
}

// ClassifyStatusComment decides what an apply run would do.
//
// Four outcomes, and the two that refuse are the interesting ones: more than
// one matching comment means this kernel cannot tell which is its own, and a
// comment somebody else authored means editing it would overwrite their words
// under our marker.
func ClassifyStatusComment(
	matches []NormalizedComment, botUsername, renderedBody string,
) (action, reason string, matched *NormalizedComment) {
	switch {
	case len(matches) == 0:
		return "create", "", nil
	case len(matches) > 1:
		return "blocked", "multiple_matches", nil
	}
	comment := matches[0]
	if !strings.EqualFold(comment.Author, botUsername) {
		return "blocked", "foreign_author", &comment
	}
	if canonicaliseForComparison(comment.Body) == canonicaliseForComparison(renderedBody) {
		return "unchanged", "", &comment
	}
	return "update", "", &comment
}

// validateForgeTarget refuses a target that names the wrong forge's flags.
//
// Both directions checked. Supplying --repo with --forge gitlab is not a
// harmless extra argument: it means the operator believes they are addressing
// something other than what this would address.
func validateForgeTarget(forge, repo string, pullRequest int, projectPath string, mergeRequest int) error {
	switch forge {
	case ForgeGitHub:
		if repo == "" || pullRequest == 0 {
			return &GateStatusError{Message: "--forge github requires --repo and --pr"}
		}
		if projectPath != "" || mergeRequest != 0 {
			return &GateStatusError{
				Message: "--project-path/--mr-iid must not be supplied with --forge github"}
		}
	case ForgeGitLab:
		if projectPath == "" || mergeRequest == 0 {
			return &GateStatusError{Message: "--forge gitlab requires --project-path and --mr-iid"}
		}
		if repo != "" || pullRequest != 0 {
			return &GateStatusError{
				Message: "--repo/--pr must not be supplied with --forge gitlab"}
		}
	default:
		return &GateStatusError{Message: fmt.Sprintf("unknown forge: %s", pythonRepr(forge))}
	}
	return nil
}
