package kernel

import (
	"fmt"
	"os"
	"strings"
)

// Publishing the gate-status comment.
//
// The shape of this is "look before writing, and look the same way in both
// modes". A dry run does everything an apply does except the write: it
// verifies the identity, pages the comments, and classifies. That is not
// wasted work -- a dry run that skipped the reads could only ever guess at
// what an apply would do, which is the one thing an operator asks it for.
//
// Where the two modes differ is what they do with a refusal. A dry run reports
// `action: "blocked"` and exits normally, because computing a diagnosis is
// safe. An apply raises, because acting on an ambiguous match is not.
//
// The one exception is the page cap, which refuses in both modes: an
// unpageable comment list means the classification itself cannot be trusted,
// and reporting an untrustworthy diagnosis is worse than declining to.

// GateStatusRequest is one `publish-gate-status` invocation.
type GateStatusRequest struct {
	Root                string
	TaskID              string
	Forge               string
	AsBot               string
	AllowClassification string
	Apply               bool
	Repo                string
	PullRequest         int
	ProjectPath         string
	MergeRequestIID     int
	BreakLock           bool
	KnowinglyMocked     bool
}

// PublishGateStatus renders the comment and, with Apply, posts it.
func (r *Registry) PublishGateStatus(request GateStatusRequest) (*orderedObject, error) {
	if err := validateForgeTarget(request.Forge, request.Repo, request.PullRequest,
		request.ProjectPath, request.MergeRequestIID); err != nil {
		return nil, err
	}
	root, err := resolveExisting(request.Root)
	if err != nil {
		return nil, &GateStatusError{Message: err.Error()}
	}
	taskID, err := SafeTaskID(request.TaskID)
	if err != nil {
		return nil, &GateStatusError{Message: err.Error()}
	}

	projection, err := r.GateStatusProjection(root, taskID)
	if err != nil {
		return nil, &GateStatusError{Message: err.Error()}
	}
	if request.AllowClassification == "" ||
		request.AllowClassification != toStringOrEmpty(projection.values["classification"]) {
		return nil, &GateStatusError{Message: fmt.Sprintf(
			"--allow-classification must be supplied and exactly match the task's classification "+
				"(got %s, task classification is %s)",
			pythonRepr(nonEmptyOrNil(request.AllowClassification)),
			pythonRepr(projection.values["classification"]))}
	}

	contracts, err := lifecycleGateContracts()
	if err != nil {
		return nil, &GateStatusError{Message: err.Error()}
	}
	renderedAt := nowRFC3339()
	marker := ComputeStatusMarker(taskID)
	body := RenderGateStatusBody(taskID, projection, contracts, renderedAt)

	adapter, err := buildStatusAdapter(request)
	if err != nil {
		return nil, err
	}
	verifiedUsername, err := adapter.VerifyIdentity(request.AsBot)
	if err != nil {
		return nil, &GateStatusError{Message: err.Error()}
	}

	// Unconditional, in both modes: this can raise the page-cap refusal, and
	// it must raise it in a dry run too.
	comments, err := adapter.ListComments()
	if err != nil {
		return nil, err
	}
	pattern := markerPattern(marker)
	var matches []NormalizedComment
	for _, comment := range comments {
		// System notes are the forge talking about itself. One can quote a
		// body containing our marker -- "changed the description" quoting the
		// old text -- and matching it would make this update a note it cannot
		// edit.
		if comment.IsSystem {
			continue
		}
		if pattern.MatchString(comment.Body) {
			matches = append(matches, comment)
		}
	}
	action, reason, matched := ClassifyStatusComment(matches, verifiedUsername, body)

	mode := "dry-run"
	if request.Apply {
		mode = "apply"
	}
	var matchedID any
	if matched != nil {
		matchedID = matched.ID
	}
	summary := ordered(
		"mode", mode,
		"task_id", taskID,
		"task_hash", TaskHash(taskID),
		"forge", request.Forge,
		"marker", marker,
		"action", action,
		"reason", nonEmptyOrNil(reason),
		"matched_comment_id", matchedID,
		"mocked", adapter.Mocked(),
		"body", body,
	)

	if !request.Apply {
		return summary, nil
	}

	// A mocked backend plus --apply is somebody about to believe a
	// publication happened. It is allowed, but only when they say they know.
	if adapter.Mocked() && !request.KnowinglyMocked {
		return nil, &GateStatusError{Message: "a mock backend env var is set but " +
			"--i-know-this-is-mocked was not passed -- refusing to --apply against a " +
			"mocked forge backend"}
	}
	if action == "blocked" {
		return nil, &GateStatusBlocked{Message: fmt.Sprintf(
			"%s: refusing to create or update a gate-status comment -- needs human resolution",
			reason)}
	}

	return r.applyGateStatus(root, taskID, request, adapter, summary,
		action, verifiedUsername, marker, body, matched)
}

// applyGateStatus performs the write and records it.
func (r *Registry) applyGateStatus(
	root, taskID string, request GateStatusRequest, adapter statusAdapter,
	summary *orderedObject, action, verifiedUsername, marker, body string,
	matched *NormalizedComment,
) (*orderedObject, error) {
	lockPath, err := LedgerPath(root, Overlay, taskID, "gate-status-"+request.Forge+".lock")
	if err != nil {
		return nil, &GateStatusError{Message: err.Error()}
	}
	if err := AcquireLockFile(lockPath, request.BreakLock); err != nil {
		return nil, &GateStatusBlocked{Message: err.Error()}
	}
	defer func() { _ = ReleaseLockFile(lockPath) }()

	ledgerPath, err := LedgerPath(root, Overlay, taskID, "gate-status-"+request.Forge+".json")
	if err != nil {
		return nil, &GateStatusError{Message: err.Error()}
	}
	ledger, err := r.loadStatusLedger(ledgerPath, taskID, request.Forge)
	if err != nil {
		return nil, &GateStatusError{Message: err.Error()}
	}
	ledger.set("target", adapter.Target())
	ledger.set("bot_username", verifiedUsername)
	ledger.set("mocked", adapter.Mocked())
	ledger.set("marker", marker)

	if action == "unchanged" {
		var commentID any
		if matched != nil {
			commentID = matched.ID
		}
		ledger.set("entries", append(listOf(ledger.values["entries"]), ordered(
			"action", "unchanged", "comment_id", commentID, "recorded_at", nowRFC3339())))
		if err := WriteLedgerFile(ledgerPath, ledger, ".gate-status."); err != nil {
			return nil, &GateStatusError{Message: err.Error()}
		}
		summary.set("comment_id", commentID)
		return summary, nil
	}

	var result NormalizedComment
	if action == "create" {
		result, err = adapter.CreateComment(body)
	} else {
		result, err = adapter.UpdateComment(matched.ID, body)
	}
	if err != nil {
		return nil, &GateStatusError{Message: err.Error()}
	}

	// Read back and check. A body the instance rewrote, or a comment
	// attributed to somebody else, means the write did not do what this
	// thinks it did -- and the ledger would otherwise record a publication
	// that did not happen as described.
	authorMatches := strings.EqualFold(result.Author, verifiedUsername)
	bodyMatches := result.Body == body
	if !authorMatches || !bodyMatches {
		ledger.set("entries", append(listOf(ledger.values["entries"]), ordered(
			"action", action, "status", "suspect", "comment_id", result.ID,
			"recorded_at", nowRFC3339(),
			"detail", "post-write verification failed: author or body mismatch after create/update")))
		// The suspect entry is written before raising, so the refusal leaves a
		// record of what was attempted rather than only a message that scrolls
		// past.
		if err := WriteLedgerFile(ledgerPath, ledger, ".gate-status."); err != nil {
			return nil, &GateStatusError{Message: err.Error()}
		}
		return nil, &GateStatusBlocked{Message: fmt.Sprintf(
			"post-write verification failed for %s on %s -- author or body did not match "+
				"after the write; aborting immediately", action, request.Forge)}
	}

	ledger.set("entries", append(listOf(ledger.values["entries"]), ordered(
		"action", action, "status", "verified", "comment_id", result.ID,
		"recorded_at", nowRFC3339())))
	if err := WriteLedgerFile(ledgerPath, ledger, ".gate-status."); err != nil {
		return nil, &GateStatusError{Message: err.Error()}
	}
	summary.set("comment_id", result.ID)
	return summary, nil
}

// loadStatusLedger reads the sidecar, or starts an empty one.
//
// Never trusted for existence. Whether a comment is already there is decided
// by scanning the live comments for the marker, every time -- a ledger can be
// stale, deleted, or from another machine, and acting on it would post a
// second comment beside one it had forgotten.
func (r *Registry) loadStatusLedger(path, taskID, forge string) (*orderedObject, error) {
	ledger := emptyCommentLedger(taskID, forge)

	data, err := os.ReadFile(path)
	if err != nil {
		// No ledger yet is the ordinary first-run case, not a failure.
		return ledger, nil
	}
	decoded, err := DecodeOrdered(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	object, ok := decoded.(*orderedObject)
	if !ok {
		return ledger, nil
	}
	// Merged onto the skeleton rather than used as-is, so a ledger written by
	// an older version still has every field this one writes.
	for _, key := range object.keys {
		ledger.set(key, object.values[key])
	}
	if _, present := ledger.values["entries"]; !present {
		ledger.set("entries", []any{})
	}
	return ledger, nil
}

// buildStatusAdapter picks the forge implementation.
func buildStatusAdapter(request GateStatusRequest) (statusAdapter, error) {
	if request.Forge == ForgeGitHub {
		client, err := NewGitHubStatusClient()
		if err != nil {
			return nil, &GateStatusError{Message: err.Error()}
		}
		return &gitHubStatusAdapter{
			client: client, repo: request.Repo, pullRequest: request.PullRequest}, nil
	}
	client, err := NewGitLabClient()
	if err != nil {
		return nil, &GateStatusError{Message: err.Error()}
	}
	return &gitLabStatusAdapter{
		client: client, projectPath: request.ProjectPath,
		mergeRequest: request.MergeRequestIID}, nil
}

type gitHubStatusAdapter struct {
	client      *GitHubStatusClient
	repo        string
	pullRequest int
}

func (a *gitHubStatusAdapter) VerifyIdentity(expected string) (string, error) {
	return a.client.VerifyIdentity(expected)
}

func (a *gitHubStatusAdapter) Mocked() bool { return a.client.Mocked() }

func (a *gitHubStatusAdapter) Target() *orderedObject {
	return ordered("repo", a.repo, "pr", a.pullRequest)
}

func (a *gitHubStatusAdapter) ListComments() ([]NormalizedComment, error) {
	var comments []NormalizedComment
	for page := 1; page <= maxCommentPages; page++ {
		raw, err := a.client.ListComments(a.repo, a.pullRequest, page, commentPageSize)
		if err != nil {
			return nil, &GateStatusError{Message: err.Error()}
		}
		for _, item := range raw {
			if object, ok := item.(map[string]any); ok {
				comments = append(comments, normaliseGitHubComment(object))
			}
		}
		if len(raw) < commentPageSize {
			return comments, nil
		}
	}
	// Refused in both modes. Beyond the cap there might be a matching comment
	// on a page nobody fetched, so the classification cannot be trusted --
	// and an untrustworthy diagnosis is worse than declining to give one.
	return nil, &GateStatusBlocked{Message: fmt.Sprintf(
		"more than %d comments on %s#%d -- cannot safely confirm whether a matching "+
			"comment exists on a later page; refusing to create or update",
		maxCommentPages*commentPageSize, a.repo, a.pullRequest)}
}

func (a *gitHubStatusAdapter) CreateComment(body string) (NormalizedComment, error) {
	commentID, err := a.client.CreateComment(a.repo, a.pullRequest, body)
	if err != nil {
		return NormalizedComment{}, err
	}
	raw, err := a.client.FetchComment(a.repo, commentID)
	if err != nil {
		return NormalizedComment{}, err
	}
	return normaliseGitHubComment(raw), nil
}

func (a *gitHubStatusAdapter) UpdateComment(commentID any, body string) (NormalizedComment, error) {
	id, ok := jsonNumber(commentID)
	if !ok {
		return NormalizedComment{}, fmt.Errorf("comment id %v is not an integer", commentID)
	}
	if err := a.client.UpdateComment(a.repo, id, body); err != nil {
		return NormalizedComment{}, err
	}
	raw, err := a.client.FetchComment(a.repo, id)
	if err != nil {
		return NormalizedComment{}, err
	}
	return normaliseGitHubComment(raw), nil
}

type gitLabStatusAdapter struct {
	client       *GitLabClient
	projectPath  string
	mergeRequest int
}

func (a *gitLabStatusAdapter) VerifyIdentity(expected string) (string, error) {
	return a.client.VerifyIdentity(expected)
}

func (a *gitLabStatusAdapter) Mocked() bool { return a.client.Mocked() }

func (a *gitLabStatusAdapter) Target() *orderedObject {
	return ordered("project_path", a.projectPath, "mr_iid", a.mergeRequest)
}

func (a *gitLabStatusAdapter) ListComments() ([]NormalizedComment, error) {
	var notes []NormalizedComment
	for page := 1; page <= maxCommentPages; page++ {
		raw, err := a.client.ListMergeRequestNotes(
			a.projectPath, a.mergeRequest, page, commentPageSize)
		if err != nil {
			return nil, &GateStatusError{Message: err.Error()}
		}
		for _, item := range raw {
			if object, ok := item.(map[string]any); ok {
				notes = append(notes, normaliseGitLabNote(object))
			}
		}
		if len(raw) < commentPageSize {
			return notes, nil
		}
	}
	return nil, &GateStatusBlocked{Message: fmt.Sprintf(
		"more than %d notes on %s MR %d -- cannot safely confirm whether a matching "+
			"note exists on a later page; refusing to create or update",
		maxCommentPages*commentPageSize, a.projectPath, a.mergeRequest)}
}

func (a *gitLabStatusAdapter) CreateComment(body string) (NormalizedComment, error) {
	noteID, err := a.client.CreateMergeRequestNote(a.projectPath, a.mergeRequest, body)
	if err != nil {
		return NormalizedComment{}, err
	}
	raw, err := a.client.FetchMergeRequestNote(a.projectPath, a.mergeRequest, noteID)
	if err != nil {
		return NormalizedComment{}, err
	}
	return normaliseGitLabNote(raw), nil
}

func (a *gitLabStatusAdapter) UpdateComment(commentID any, body string) (NormalizedComment, error) {
	id, ok := jsonNumber(commentID)
	if !ok {
		return NormalizedComment{}, fmt.Errorf("note id %v is not an integer", commentID)
	}
	if err := a.client.UpdateMergeRequestNote(a.projectPath, a.mergeRequest, id, body); err != nil {
		return NormalizedComment{}, err
	}
	raw, err := a.client.FetchMergeRequestNote(a.projectPath, a.mergeRequest, id)
	if err != nil {
		return NormalizedComment{}, err
	}
	return normaliseGitLabNote(raw), nil
}
