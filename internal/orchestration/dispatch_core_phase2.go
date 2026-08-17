package orchestration

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Phase 2: Main Dispatch Function, Confirmation Gating, Async Job Stores

var (
	globalJobStore       = NewDispatchJobStore()
	globalJobStoreMu     sync.Mutex
	globalTeamJobStore   = NewTeamDispatchJobStore()
	globalTeamJobStoreMu sync.Mutex
)

// DispatchRoots are the three tiers a role file is looked up in.
//
// Passed explicitly rather than resolved inside the dispatch functions so the
// compiler requires every caller to supply them. They used not to be passed
// at all, which is how dispatch came to run without ever opening a role file.
type DispatchRoots struct {
	ProjectRoot string
	GlobalRoot  string
	PluginRoot  string
}

// DispatchSecureCloudRole is the main dispatch entry point
// Validates inputs, resolves role, spawns child, captures output, logs audit
func DispatchSecureCloudRole(
	roots DispatchRoots,
	roleID, brief, mode, classification, confirmationToken, taskID, sessionID, parentClassification, runner string,
	wait bool,
) map[string]any {
	// Input validation
	if roleID == "" {
		return auditDecision(roleID, taskID, sessionID, classification, mode, runner,
			map[string]any{
				"status": "denied",
				"reason": "role_id is required",
			})
	}

	if brief == "" {
		return auditDecision(roleID, taskID, sessionID, classification, mode, runner,
			map[string]any{
				"status": "denied",
				"reason": "brief is required",
			})
	}

	if len(brief) > MaxBriefBytes {
		return auditDecision(roleID, taskID, sessionID, classification, mode, runner,
			map[string]any{
				"status": "denied",
				"reason": fmt.Sprintf("brief exceeds maximum size %d bytes", MaxBriefBytes),
			})
	}

	// Validate mode
	if mode != ModePlanningOnly && mode != ModeRepositoryEdit {
		return auditDecision(roleID, taskID, sessionID, classification, mode, runner,
			map[string]any{
				"status": "denied",
				"reason": fmt.Sprintf("mode must be '%s' or '%s'", ModePlanningOnly, ModeRepositoryEdit),
			})
	}

	// Validate classification
	if !Classifications[classification] {
		return auditDecision(roleID, taskID, sessionID, classification, mode, runner,
			map[string]any{
				"status": "denied",
				"reason": fmt.Sprintf("invalid classification: %q", classification),
			})
	}

	// Validate dispatch depth
	currentDepth := currentDispatchDepth()
	if currentDepth >= MaxDispatchDepth {
		return auditDecision(roleID, taskID, sessionID, classification, mode, runner,
			map[string]any{
				"status": "denied",
				"reason": fmt.Sprintf("dispatch depth %d exceeds maximum %d", currentDepth, MaxDispatchDepth),
			})
	}

	// The role is resolved before anything is decided about it. Its
	// sandbox_mode is an input to the effective sandbox, which is what
	// decides whether human confirmation is required -- so a dispatch that
	// has not read its role file cannot know whether it needs a gate.
	//
	// This previously called ComputeEffectiveSandbox(mode, "") with a
	// hard-coded empty sandbox mode, which always resolved to read-only, so
	// the write-capable branch below never fired and no dispatch ever asked
	// for confirmation.
	if runner == "" {
		runner = DefaultRunner
	}
	role, err := ResolveRoleForDispatch(roleID, runner, roots.ProjectRoot, roots.GlobalRoot, roots.PluginRoot, mode)
	if err != nil {
		return auditDecision(roleID, taskID, sessionID, classification, mode, runner,
			dispatchErrorResult(err))
	}

	effectiveSandbox, err := EffectiveSandboxForDispatch(role, mode)
	if err != nil {
		return auditDecision(roleID, taskID, sessionID, classification, mode, runner,
			map[string]any{
				"status": "denied",
				"reason": err.Error(),
			})
	}

	gate := NewConfirmationGate()
	if WriteCarpableSandboxes[effectiveSandbox] && confirmationToken == "" {
		// First call - generate token and request confirmation
		token, err := gate.RequestConfirmation(map[string]any{
			"role_id":        roleID,
			"brief":          brief,
			"mode":           mode,
			"classification": classification,
			"task_id":        taskID,
		})
		if err != nil {
			return map[string]any{
				"status": "error",
				"reason": fmt.Sprintf("confirmation gate error: %v", err),
			}
		}

		return map[string]any{
			"status":             "confirmation_required",
			"confirmation_token": token,
			"sandbox_mode":       effectiveSandbox,
			"message":            "Write-capable sandbox requires human confirmation. Replay with confirmation_token to proceed.",
		}
	}

	// If we have a write-capable sandbox, validate confirmation token
	if WriteCarpableSandboxes[effectiveSandbox] && confirmationToken != "" {
		_, err := gate.ValidateConfirmation(confirmationToken)
		if err != nil {
			return map[string]any{
				"status": "denied",
				"reason": fmt.Sprintf("confirmation token invalid or expired: %v", err),
			}
		}
	}

	// The dispatch context carries the composed prompt: the role's own
	// instructions, then the caller's brief behind an unforgeable fence.
	dispatchCtx, err := BuildDispatchContext(roleID, role, brief, mode)
	if err != nil {
		return map[string]any{"status": "denied", "reason": err.Error()}
	}
	dispatchCtx.Sandbox = effectiveSandbox
	dispatchCtx.ProjectRoot = roots.ProjectRoot

	if !wait {
		return dispatchAsync(roots, dispatchCtx, role, classification, parentClassification, taskID, sessionID, runner, effectiveSandbox, mode)
	}
	return dispatchSync(roots, dispatchCtx, role, classification, parentClassification, taskID, sessionID, runner, effectiveSandbox, mode)
}

// auditDecision records a dispatch outcome that never reached a child.
//
// Audit records were written only after a child ran, so every refusal --
// a role the catalog does not name, a tampered or uncommitted role file, a
// classification above the ceiling, a depth or concurrency cap -- left no
// trace at all. Refusals are exactly what an auditor most needs to see, and
// a log that records only successes cannot show an attempt that was stopped.
func auditDecision(roleID, taskID, sessionID, classification, mode, runner string, result map[string]any) map[string]any {
	fields := map[string]any{
		"event":          "dispatch",
		"role_id":        roleID,
		"task_id":        taskID,
		"session_id":     sessionID,
		"classification": classification,
		"mode":           mode,
		"runner":         runner,
	}
	if status, ok := result["status"].(string); ok {
		fields["decision"] = status
	}
	// The reason is this module's own generated wording, never the brief,
	// the instructions, or the child's output -- BuildAuditRecord rejects
	// those keys outright.
	if reason, ok := result["reason"].(string); ok {
		fields["reason"] = reason
	}
	if record, err := BuildAuditRecord(fields); err == nil {
		writeAuditBestEffort(record)
	}
	return result
}

// writeAuditBestEffort writes a record without ever failing the dispatch.
//
// Used where a side effect has already happened, or where the caller must
// still get a terminal answer: a missing audit line for one event is better
// than a caller with no way to observe that the event happened at all. The
// failure is reported to stderr rather than swallowed, so an operator has a
// trace to grep for.
func writeAuditBestEffort(record map[string]any) {
	if err := WriteAuditLog(record); err != nil {
		fmt.Fprintf(os.Stderr, "cadre: audit write failed (decision=%v id=%v): %v\n",
			record["decision"], auditTraceID(record), err)
	}
}

// auditTraceID names the record in the stderr trace, falling back through the
// identifiers a record might carry so the line is never anonymous.
func auditTraceID(record map[string]any) any {
	for _, key := range []string{"job_id", "team_id", "task_id", "role_id"} {
		if value, ok := record[key]; ok && value != "" {
			return value
		}
	}
	return "unknown"
}

// dispatchErrorResult maps a resolution failure onto the result vocabulary.
//
// Denied and unavailable are different answers: denied means the request was
// refused on its merits (a tampered role file, an uncommitted one in write
// mode), unavailable means the role could not be found at all. Collapsing
// them would tell a caller to fix the wrong thing.
func dispatchErrorResult(err error) map[string]any {
	var unavailable *DispatchUnavailable
	if errors.As(err, &unavailable) {
		return map[string]any{"status": "unavailable", "reason": unavailable.Reason}
	}
	var denied *DispatchDenied
	if errors.As(err, &denied) {
		return map[string]any{"status": "denied", "reason": denied.Reason}
	}
	var notClean *ProjectTierNotGitCleanError
	if errors.As(err, &notClean) {
		return map[string]any{"status": "denied", "reason": notClean.Reason}
	}
	return map[string]any{"status": "denied", "reason": err.Error()}
}

// recordDispatchAudit writes the record that says which role text ran.
//
// resolved_path and resolution_tier say where the file was; instructions_
// sha256 says what it contained, which is the only field that survives the
// file being edited afterwards. Mirrors the Python original's field set.
func recordDispatchAudit(
	role *ResolvedRole,
	roleID, taskID, sessionID, classification, mode, effectiveSandbox, runner string,
	result map[string]any,
) {
	fields := map[string]any{
		"event":               "dispatch",
		"role_id":             roleID,
		"task_id":             taskID,
		"session_id":          sessionID,
		"classification":      classification,
		"mode":                mode,
		"runner":              runner,
		"effective_sandbox":   effectiveSandbox,
		"resolved_path":       role.FilePath,
		"resolution_tier":     role.Tier,
		"model":               role.Model,
		"instructions_sha256": role.InstructionsSHA256,
	}
	for _, key := range []string{"exit_code", "timed_out", "duration_seconds", "pid", "status"} {
		if value, ok := result[key]; ok {
			fields[key] = value
		}
	}
	record, err := BuildAuditRecord(fields)
	if err != nil {
		return
	}
	writeAuditBestEffort(record)
}

// dispatchSync runs the child and waits for it.
//
// This used to compose a prompt from the literal string "Role instructions
// would be loaded from role file here" and spawn `echo`, then report
// {"status": "success", "exit_code": 0}. The role file was never opened, so
// every guard in front of it -- tier resolution, the git-clean gate, symlink
// refusal, sandbox narrowing -- was unreachable from the MCP tool that is the
// only production caller.
func dispatchSync(
	roots DispatchRoots,
	dispatchCtx *DispatchContext,
	role *ResolvedRole,
	classification, parentClassification, taskID, sessionID, runner, effectiveSandbox, mode string,
) map[string]any {
	if !currentDispatchLimiter().TryAcquire() {
		return map[string]any{
			"status": "denied",
			"reason": fmt.Sprintf("too many concurrent dispatches (limit %d); retry later", MaxConcurrentChildren),
		}
	}
	defer currentDispatchLimiter().Release()

	env := BuildChildEnv(currentDispatchDepth()+1, roots.ProjectRoot)
	env[ParentClassificationVar] = classification

	result := ExecuteDispatchChild(dispatchCtx, runner, env, DefaultTimeoutSeconds)
	// Capture before the audit, so the audit record can say whether the
	// handoff was stored -- and best-effort, because a capture that failed
	// must not change whether the child completed.
	result["context_capture"] = AutomaticContextCapture(roots.ProjectRoot,
		result, dispatchCtx.RoleID, taskID, sessionID, parentClassification, classification)
	recordDispatchAudit(role, dispatchCtx.RoleID, taskID, sessionID,
		classification, mode, effectiveSandbox, runner, result)
	return result
}

// dispatchAsync starts the child and returns a job id to poll.
func dispatchAsync(
	roots DispatchRoots,
	dispatchCtx *DispatchContext,
	role *ResolvedRole,
	classification, parentClassification, taskID, sessionID, runner, effectiveSandbox, mode string,
) map[string]any {
	jobID, err := generateJobID()
	if err != nil {
		return map[string]any{
			"status": "error",
			"reason": fmt.Sprintf("failed to generate job ID: %v", err),
		}
	}

	// The slot is taken before returning, not inside the goroutine: an async
	// dispatch that reported a job id and then queued behind the limiter
	// would have told the caller work had started when it had not.
	if !currentDispatchLimiter().TryAcquire() {
		return map[string]any{
			"status": "denied",
			"reason": fmt.Sprintf("too many concurrent dispatches (limit %d); retry later", MaxConcurrentChildren),
		}
	}

	env := BuildChildEnv(currentDispatchDepth()+1, roots.ProjectRoot)
	env[ParentClassificationVar] = classification

	go func() {
		defer currentDispatchLimiter().Release()
		result := ExecuteDispatchChild(dispatchCtx, runner, env, DefaultTimeoutSeconds)
		result["context_capture"] = AutomaticContextCapture(roots.ProjectRoot,
			result, dispatchCtx.RoleID, taskID, sessionID, parentClassification, classification)
		recordDispatchAudit(role, dispatchCtx.RoleID, taskID, sessionID,
			classification, mode, effectiveSandbox, runner, result)

		globalJobStoreMu.Lock()
		globalJobStore.RecordJob(jobID, result)
		globalJobStoreMu.Unlock()
	}()

	return map[string]any{
		"status":  "dispatched_async",
		"job_id":  jobID,
		"message": "Dispatch started in background. Poll with poll_dispatch_status to check progress.",
	}
}

// PollDispatchStatus polls the status of an async dispatch job
func PollDispatchStatus(jobID string) map[string]any {
	globalJobStoreMu.Lock()
	result := globalJobStore.GetJob(jobID)
	globalJobStoreMu.Unlock()

	if result == nil {
		return map[string]any{
			"status": "not_found",
			"job_id": jobID,
		}
	}

	return result
}

// DispatchTeam dispatches multiple roles as a team
func DispatchTeam(
	roots DispatchRoots,
	members []map[string]string,
	mode, classification, confirmationToken, taskID, sessionID, parentClassification, runner string,
	wait bool,
) map[string]any {
	if len(members) == 0 {
		return map[string]any{
			"status": "denied",
			"reason": "members list cannot be empty",
		}
	}

	if len(members) > 8 {
		return map[string]any{
			"status": "denied",
			"reason": "team size limited to 8 members maximum",
		}
	}

	// Validate mode
	if mode != ModePlanningOnly && mode != ModeRepositoryEdit {
		return map[string]any{
			"status": "denied",
			"reason": fmt.Sprintf("mode must be '%s' or '%s'", ModePlanningOnly, ModeRepositoryEdit),
		}
	}

	// Check if any member will be write-capable
	effectiveSandbox, _, _ := ComputeEffectiveSandbox(mode, "")
	needsConfirmation := WriteCarpableSandboxes[effectiveSandbox]

	// Confirmation gate for write-capable team dispatch
	gate := NewTeamConfirmationGate()
	if needsConfirmation && confirmationToken == "" {
		token, err := gate.RequestConfirmation(map[string]any{
			"members": members,
			"mode":    mode,
			"task_id": taskID,
		})
		if err != nil {
			return map[string]any{
				"status": "error",
				"reason": fmt.Sprintf("confirmation gate error: %v", err),
			}
		}

		return map[string]any{
			"status":                     "confirmation_required",
			"confirmation_token":         token,
			"members_requiring_approval": len(members),
			"message":                    "Write-capable team dispatch requires confirmation. Replay with confirmation_token to proceed.",
		}
	}

	// Dispatch team members concurrently (up to MAX_CONCURRENT_CHILDREN)
	teamID, err := generateJobID()
	if err != nil {
		return map[string]any{
			"status": "error",
			"reason": fmt.Sprintf("failed to generate team ID: %v", err),
		}
	}

	if !wait {
		// Async team dispatch
		go dispatchTeamAsync(roots, teamID, members, mode, classification, taskID, sessionID, parentClassification, runner)
		return map[string]any{
			"status":  "team_dispatched_async",
			"team_id": teamID,
			"message": "Team dispatch started in background. Poll with poll_team_status to check progress.",
		}
	}

	// Sync team dispatch
	results := dispatchTeamSync(roots, teamID, members, mode, classification, taskID, sessionID, parentClassification, runner)
	return results
}

func dispatchTeamSync(roots DispatchRoots, teamID string, members []map[string]string, mode, classification, taskID, sessionID, parentClassification, runner string) map[string]any {
	memberResults := make([]map[string]any, 0, len(members))

	// Dispatch members concurrently with limit
	limiter := NewConcurrencyLimiter(MaxConcurrentChildren)
	resultsChan := make(chan map[string]any, len(members))

	for i, member := range members {
		limiter.Acquire()
		go func(idx int, m map[string]string) {
			defer limiter.Release()

			roleID := m["role_id"]
			brief := m["brief"]
			if brief == "" {
				brief = "No brief provided"
			}

			result := DispatchSecureCloudRole(roots, roleID, brief, mode, classification, "", taskID, sessionID, parentClassification, runner, true)
			result["member_index"] = idx
			result["role_id"] = roleID
			resultsChan <- result
		}(i, member)
	}

	// Collect results
	for i := 0; i < len(members); i++ {
		memberResults = append(memberResults, <-resultsChan)
	}

	globalTeamJobStoreMu.Lock()
	globalTeamJobStore.RecordTeamJob(teamID, map[string]any{
		"status":  "team_dispatched",
		"team_id": teamID,
		"members": memberResults,
		"task_id": taskID,
	})
	globalTeamJobStoreMu.Unlock()

	return map[string]any{
		"status":  "team_dispatched",
		"team_id": teamID,
		"members": memberResults,
	}
}

func dispatchTeamAsync(roots DispatchRoots, teamID string, members []map[string]string, mode, classification, taskID, sessionID, parentClassification, runner string) {
	result := dispatchTeamSync(roots, teamID, members, mode, classification, taskID, sessionID, parentClassification, runner)
	globalTeamJobStoreMu.Lock()
	globalTeamJobStore.RecordTeamJob(teamID, result)
	globalTeamJobStoreMu.Unlock()
}

// PollTeamStatus polls the status of a team dispatch
func PollTeamStatus(teamID string) map[string]any {
	globalTeamJobStoreMu.Lock()
	result := globalTeamJobStore.GetTeamJob(teamID)
	globalTeamJobStoreMu.Unlock()

	if result == nil {
		return map[string]any{
			"status":  "not_found",
			"team_id": teamID,
		}
	}

	return result
}

// TeamConfirmationGate manages confirmation for team dispatch
type TeamConfirmationGate struct {
	mu    sync.Mutex
	token string
	data  map[string]any
}

func NewTeamConfirmationGate() *TeamConfirmationGate {
	return &TeamConfirmationGate{
		data: make(map[string]any),
	}
}

func (tcg *TeamConfirmationGate) RequestConfirmation(data map[string]any) (string, error) {
	token, err := generateConfirmationToken()
	if err != nil {
		return "", err
	}

	tcg.mu.Lock()
	tcg.token = token
	tcg.data = data
	tcg.mu.Unlock()

	return token, nil
}

func (tcg *TeamConfirmationGate) ValidateConfirmation(token string) error {
	tcg.mu.Lock()
	defer tcg.mu.Unlock()

	if token != tcg.token {
		return fmt.Errorf("invalid confirmation token")
	}

	tcg.token = ""
	return nil
}

// TeamDispatchJobStore manages team job lifecycle
type TeamDispatchJobStore struct {
	mu   sync.Mutex
	jobs map[string]map[string]any
}

func NewTeamDispatchJobStore() *TeamDispatchJobStore {
	return &TeamDispatchJobStore{
		jobs: make(map[string]map[string]any),
	}
}

func (ts *TeamDispatchJobStore) RecordTeamJob(teamID string, result map[string]any) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.jobs[teamID] = result
}

func (ts *TeamDispatchJobStore) GetTeamJob(teamID string) map[string]any {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	job, ok := ts.jobs[teamID]
	if !ok {
		return nil
	}

	// Check TTL
	if recorded, ok := job["timestamp"].(time.Time); ok {
		if time.Since(recorded) > DispatchJobTTLSeconds*time.Second {
			delete(ts.jobs, teamID)
			return nil
		}
	}

	return job
}

// ConcurrencyLimiter restricts concurrent goroutine execution
type ConcurrencyLimiter struct {
	semaphore chan struct{}
}

func NewConcurrencyLimiter(maxConcurrent int) *ConcurrencyLimiter {
	return &ConcurrencyLimiter{
		semaphore: make(chan struct{}, maxConcurrent),
	}
}

func (cl *ConcurrencyLimiter) Acquire() {
	cl.semaphore <- struct{}{}
}

// TryAcquire takes a slot if one is free and reports whether it did.
//
// Dispatch needs the non-blocking form: a caller waiting on a channel is a
// tool call that never returns, and "too many concurrent dispatches, retry
// later" is an answer the caller can act on.
func (cl *ConcurrencyLimiter) TryAcquire() bool {
	select {
	case cl.semaphore <- struct{}{}:
		return true
	default:
		return false
	}
}

func (cl *ConcurrencyLimiter) Release() {
	<-cl.semaphore
}

// dispatchLimiter caps how many children this process runs at once, across
// every dispatch path -- single, team, sync and async alike. Process-wide
// because the resource it protects is the machine, not one call.
//
// Held behind an atomic pointer because a test binary replaces it: the
// limiter is process-wide, so a test that dispatches would otherwise inherit
// whatever slots earlier tests leaked (see dispatch_limiter_isolation_test.go).
// A plain variable races -- the dispatch paths read it from goroutines while
// the replacement is written -- which the race detector catches and a
// non-race run does not.
var dispatchLimiter atomic.Pointer[ConcurrencyLimiter]

func init() {
	dispatchLimiter.Store(NewConcurrencyLimiter(MaxConcurrentChildren))
}

// currentDispatchLimiter is the limiter every dispatch path acquires from.
func currentDispatchLimiter() *ConcurrencyLimiter { return dispatchLimiter.Load() }

// currentDispatchDepth reads the nesting counter a parent set for its child.
//
// An unparseable value is treated as already at the limit, not as zero. The
// counter exists to stop a dispatch chain recursing without bound, and the
// Atoi error was discarded here -- so a garbage value (including one a child
// could set for a grandchild) read as depth 0 and reset the limit that
// variable exists to enforce.
func currentDispatchDepth() int {
	raw := os.Getenv(DepthEnvVar)
	if raw == "" {
		return 0
	}
	depth, err := strconv.Atoi(raw)
	if err != nil {
		return MaxDispatchDepth
	}
	return depth
}
