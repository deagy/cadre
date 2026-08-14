package orchestration

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"
)

// Phase 2: Main Dispatch Function, Confirmation Gating, Async Job Stores

var (
	globalJobStore       = NewDispatchJobStore()
	globalJobStoreMu     sync.Mutex
	globalTeamJobStore   = NewTeamDispatchJobStore()
	globalTeamJobStoreMu sync.Mutex
)

// DispatchSecureCloudRole is the main dispatch entry point
// Validates inputs, resolves role, spawns child, captures output, logs audit
func DispatchSecureCloudRole(
	roleID, brief, mode, classification, confirmationToken, taskID, sessionID, parentClassification, runner string,
	wait bool,
) map[string]any {
	// Input validation
	if roleID == "" {
		return map[string]any{
			"status": "denied",
			"reason": "role_id is required",
		}
	}

	if brief == "" {
		return map[string]any{
			"status": "denied",
			"reason": "brief is required",
		}
	}

	if len(brief) > MaxBriefBytes {
		return map[string]any{
			"status": "denied",
			"reason": fmt.Sprintf("brief exceeds maximum size %d bytes", MaxBriefBytes),
		}
	}

	// Validate mode
	if mode != ModePlanningOnly && mode != ModeRepositoryEdit {
		return map[string]any{
			"status": "denied",
			"reason": fmt.Sprintf("mode must be '%s' or '%s'", ModePlanningOnly, ModeRepositoryEdit),
		}
	}

	// Validate classification
	if !Classifications[classification] {
		return map[string]any{
			"status": "denied",
			"reason": fmt.Sprintf("invalid classification: %q", classification),
		}
	}

	// Validate dispatch depth
	currentDepth := currentDispatchDepth()
	if currentDepth >= MaxDispatchDepth {
		return map[string]any{
			"status": "denied",
			"reason": fmt.Sprintf("dispatch depth %d exceeds maximum %d", currentDepth, MaxDispatchDepth),
		}
	}

	// Check confirmation requirement (write-capable mode needs token replay)
	effectiveSandbox, _, err := ComputeEffectiveSandbox(mode, "")
	if err != nil {
		return map[string]any{
			"status": "denied",
			"reason": err.Error(),
		}
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

	// Dispatch logic for async vs sync
	if !wait {
		// Async dispatch - return job ID immediately
		return dispatchAsync(roleID, brief, mode, classification, taskID, sessionID, parentClassification, runner, gate)
	}

	// Sync dispatch - wait for completion
	return dispatchSync(roleID, brief, mode, classification, taskID, sessionID, parentClassification, runner, gate)
}

// dispatchSync handles synchronous dispatch
func dispatchSync(
	roleID, brief, mode, classification, taskID, sessionID, parentClassification, runner string,
	gate *ConfirmationGate,
) map[string]any {
	// Build child environment
	env := BuildChildEnv(currentDispatchDepth()+1, "")
	env[ParentClassificationVar] = classification

	// Compose prompt
	developerInstructions := "Role instructions would be loaded from role file here"
	prompt := ComposePrompt(developerInstructions, brief)

	// Spawn and wait for child
	result, err := SpawnAndWait("echo", []string{prompt}, env, DefaultTimeoutSeconds)
	if err != nil {
		return map[string]any{
			"status": "error",
			"reason": fmt.Sprintf("child process error: %v", err),
		}
	}

	// Log audit record
	exitCode := -1
	if ec, ok := result["exit_code"]; ok {
		if v, ok := ec.(int); ok {
			exitCode = v
		}
	}
	auditRecord, err := BuildAuditRecord(map[string]any{
		"role_id":        roleID,
		"task_id":        taskID,
		"session_id":     sessionID,
		"classification": classification,
		"mode":           mode,
		"sandbox_mode":   result["status"],
		"exit_code":      exitCode,
	})
	if err == nil {
		_ = WriteAuditLog(auditRecord)
	}

	return map[string]any{
		"status": "success",
		"result": result,
	}
}

// dispatchAsync handles asynchronous dispatch
func dispatchAsync(
	roleID, brief, mode, classification, taskID, sessionID, parentClassification, runner string,
	gate *ConfirmationGate,
) map[string]any {
	// Generate job ID
	jobID, err := generateJobID()
	if err != nil {
		return map[string]any{
			"status": "error",
			"reason": fmt.Sprintf("failed to generate job ID: %v", err),
		}
	}

	// Spawn in background (stub - would spawn actual subprocess)
	go func() {
		env := BuildChildEnv(currentDispatchDepth()+1, "")
		env[ParentClassificationVar] = classification

		developerInstructions := "Role instructions would be loaded from role file here"
		prompt := ComposePrompt(developerInstructions, brief)

		result, _ := SpawnAndWait("echo", []string{prompt}, env, DefaultTimeoutSeconds)

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
		go dispatchTeamAsync(teamID, members, mode, classification, taskID, sessionID, parentClassification, runner)
		return map[string]any{
			"status":  "team_dispatched_async",
			"team_id": teamID,
			"message": "Team dispatch started in background. Poll with poll_team_status to check progress.",
		}
	}

	// Sync team dispatch
	results := dispatchTeamSync(teamID, members, mode, classification, taskID, sessionID, parentClassification, runner)
	return results
}

func dispatchTeamSync(teamID string, members []map[string]string, mode, classification, taskID, sessionID, parentClassification, runner string) map[string]any {
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

			result := DispatchSecureCloudRole(roleID, brief, mode, classification, "", taskID, sessionID, parentClassification, runner, true)
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

func dispatchTeamAsync(teamID string, members []map[string]string, mode, classification, taskID, sessionID, parentClassification, runner string) {
	result := dispatchTeamSync(teamID, members, mode, classification, taskID, sessionID, parentClassification, runner)
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

func (cl *ConcurrencyLimiter) Release() {
	<-cl.semaphore
}

// Helper to get current dispatch depth from environment
func currentDispatchDepth() int {
	depthStr := os.Getenv(DepthEnvVar)
	if depthStr == "" {
		return 0
	}
	depth, _ := strconv.Atoi(depthStr)
	return depth
}
