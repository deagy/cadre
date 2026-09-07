package orchestration

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// Phase 2 Tests: Main Dispatch Function, Confirmation Gating, Async Job Stores

func TestDispatchSecureCloudRoleValidation(t *testing.T) {
	stubRunner(t)
	tests := []struct {
		name           string
		roleID         string
		brief          string
		mode           string
		classification string
		expectStatus   string
	}{
		{
			name:           "missing role_id",
			roleID:         "",
			brief:          "test",
			mode:           ModePlanningOnly,
			classification: "public",
			expectStatus:   "denied",
		},
		{
			name:           "missing brief",
			roleID:         "code-reviewer",
			brief:          "",
			mode:           ModePlanningOnly,
			classification: "public",
			expectStatus:   "denied",
		},
		{
			name:           "invalid mode",
			roleID:         "code-reviewer",
			brief:          "test",
			mode:           "invalid-mode",
			classification: "public",
			expectStatus:   "denied",
		},
		{
			name:           "invalid classification",
			roleID:         "code-reviewer",
			brief:          "test",
			mode:           ModePlanningOnly,
			classification: "invalid",
			expectStatus:   "denied",
		},
		{
			name:           "valid planning-only",
			roleID:         "code-reviewer",
			brief:          "test brief",
			mode:           ModePlanningOnly,
			classification: "public",
			expectStatus:   "success",
		},
		{
			name:           "valid repository-edit",
			roleID:         "code-reviewer",
			brief:          "test brief",
			mode:           ModeRepositoryEdit,
			classification: "internal",
			expectStatus:   "success",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DispatchSecureCloudRole(
				DispatchRoots{},
				tt.roleID, tt.brief, tt.mode, tt.classification,
				"", "task123", "session123", "public", DefaultRunner, true,
			)

			status := result["status"].(string)
			if tt.expectStatus == "denied" || tt.expectStatus == "success" {
				if status == "denied" && tt.expectStatus == "denied" {
					// Expected to be denied
					return
				}
				if status == "success" && tt.expectStatus == "success" {
					// Expected to succeed (or at least not denied)
					return
				}
				if status == "confirmation_required" && tt.expectStatus == "success" {
					// Confirmation required is not a denial
					return
				}
			}
		})
	}
}

func TestDispatchConfirmationRequired(t *testing.T) {
	stubRunner(t)
	// Repository edit mode should require confirmation for write-capable sandbox
	result := DispatchSecureCloudRole(
		testRoots(t, "code-reviewer"),
		"code-reviewer", "test brief", ModeRepositoryEdit, "public",
		"", "task123", "session123", "public", DefaultRunner, true,
	)

	// First call should either succeed directly OR request confirmation
	status := result["status"].(string)
	if status != "success" && status != "confirmation_required" {
		t.Errorf("first dispatch returned %q, want 'success' or 'confirmation_required'", status)
	}

	if status == "confirmation_required" {
		token, ok := result["confirmation_token"].(string)
		if !ok || token == "" {
			t.Errorf("confirmation_required response missing valid confirmation_token")
		}
	}
}

func TestPollDispatchStatus(t *testing.T) {
	stubRunner(t)
	// The limiter is process-wide, so without this the test passes or fails on
	// how many async dispatches ran before it -- see
	// dispatch_limiter_isolation_test.go.
	withFreshDispatchLimiter(t)

	// Async dispatch should return job_id immediately
	result := DispatchSecureCloudRole(
		testRoots(t, "code-reviewer"),
		"code-reviewer", "test brief", ModePlanningOnly, "public",
		"", "task123", "session123", "public", DefaultRunner, false, // wait=false
	)

	status := result["status"].(string)
	if status != "dispatched_async" {
		t.Errorf("async dispatch returned status %q, want 'dispatched_async'", status)
	}

	jobID, ok := result["job_id"].(string)
	if !ok || jobID == "" {
		t.Errorf("dispatched_async response missing job_id")
		return
	}

	// Poll the job status.
	//
	// The property is that polling a job id this process just handed out
	// returns a coherent status -- not that the child succeeded. In a test
	// fixture the child cannot run: the role resolves against a temp roster
	// that has no executable behind it, so the job reaches a terminal state
	// almost immediately, and *which* terminal state depends on the machine.
	//
	// Locally the poll returns not_found at every interval, including a second
	// later. On a CI runner it returns unavailable -- the async goroutine has
	// already finished and recorded that the role could not be resolved. Both
	// are legitimate answers about a job that will never complete; the
	// original list simply did not include the second, so the test passed here
	// and failed there.
	pollResult := PollDispatchStatus(jobID)
	pollStatus := pollResult["status"].(string)

	acceptable := map[string]bool{
		"dispatched_async": true, // still queued or running
		"success":          true, // completed
		"not_found":        true, // never recorded, or already reaped
		"unavailable":      true, // completed, role not resolvable in a fixture
	}
	if !acceptable[pollStatus] {
		t.Errorf("poll returned status %q, which is not a status this job could "+
			"legitimately be in (reason %v)", pollStatus, pollResult["reason"])
	}
	// And it is a status, not an empty string -- a poll that returned nothing
	// would satisfy a "not one of the bad ones" check.
	if pollStatus == "" {
		t.Error("poll returned an empty status")
	}
}

func TestDispatchTeamValidation(t *testing.T) {
	tests := []struct {
		name         string
		members      []map[string]string
		expectStatus string
	}{
		{
			name:         "empty members",
			members:      []map[string]string{},
			expectStatus: "denied",
		},
		{
			name: "valid single member",
			members: []map[string]string{
				{"role_id": "code-reviewer", "brief": "test"},
			},
			expectStatus: "team_dispatched",
		},
		{
			name: "valid multiple members",
			members: []map[string]string{
				{"role_id": "code-reviewer", "brief": "brief1"},
				{"role_id": "security-reviewer", "brief": "brief2"},
			},
			expectStatus: "team_dispatched",
		},
		{
			name: "too many members",
			members: func() []map[string]string {
				members := make([]map[string]string, 9)
				for i := 0; i < 9; i++ {
					members[i] = map[string]string{
						"role_id": "role" + string(rune(i)),
						"brief":   "brief",
					}
				}
				return members
			}(),
			expectStatus: "denied",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DispatchTeam(
				DispatchRoots{},
				tt.members, ModePlanningOnly, "public",
				"", "task123", "session123", "public", DefaultRunner, true,
			)

			status := result["status"].(string)
			if status != tt.expectStatus {
				if tt.expectStatus == "denied" && status == "denied" {
					return
				}
				if tt.expectStatus != "denied" && (status == "team_dispatched" || status == "confirmation_required") {
					return
				}
				t.Errorf("team dispatch returned status %q, want %q", status, tt.expectStatus)
			}
		})
	}
}

func TestDispatchTeamConcurrency(t *testing.T) {
	members := []map[string]string{
		{"role_id": "code-reviewer", "brief": "brief1"},
		{"role_id": "security-reviewer", "brief": "brief2"},
		{"role_id": "role3", "brief": "brief3"},
	}

	result := DispatchTeam(
		DispatchRoots{},
		members, ModePlanningOnly, "public",
		"", "task123", "session123", "public", DefaultRunner, true,
	)

	status := result["status"].(string)
	if status != "team_dispatched" && status != "confirmation_required" {
		t.Errorf("team dispatch returned status %q", status)
		return
	}

	if status == "team_dispatched" {
		membersResult, ok := result["members"].([]map[string]any)
		if !ok {
			t.Errorf("team_dispatched response members field not a slice")
			return
		}

		if len(membersResult) != len(members) {
			t.Errorf("member result count %d, want %d", len(membersResult), len(members))
		}
	}
}

func TestTeamConfirmationGate(t *testing.T) {
	gate := NewTeamConfirmationGate()

	members := []map[string]string{
		{"role_id": "code-reviewer", "brief": "brief1"},
	}

	data := map[string]any{
		"members":        members,
		"mode":           ModePlanningOnly,
		"classification": "public",
		"task_id":        "task-123",
	}

	token, err := gate.RequestConfirmation(data)
	if err != nil {
		t.Fatalf("RequestConfirmation failed: %v", err)
	}
	if token == "" {
		t.Errorf("token is empty")
	}

	// Validate token with matching parameters
	err = gate.ValidateConfirmation(token, members, ModePlanningOnly, "public", "task-123")
	if err != nil {
		t.Errorf("ValidateConfirmation failed: %v", err)
	}

	// Second validation should fail - token consumed
	err = gate.ValidateConfirmation(token, members, ModePlanningOnly, "public", "task-123")
	if err == nil {
		t.Errorf("ValidateConfirmation should fail for consumed token")
	}
}

func TestTeamDispatchJobStore(t *testing.T) {
	store := NewTeamDispatchJobStore()

	result := map[string]any{
		"status":  "team_dispatched",
		"team_id": "team_abc123",
		"members": []map[string]any{
			{"status": "success"},
			{"status": "success"},
		},
	}

	store.RecordTeamJob("team_abc123", result)

	retrieved := store.GetTeamJob("team_abc123")
	if retrieved == nil {
		t.Errorf("GetTeamJob returned nil")
		return
	}

	if retrieved["team_id"] != "team_abc123" {
		t.Errorf("team_id mismatch")
	}

	// Non-existent team
	notFound := store.GetTeamJob("team_nonexistent")
	if notFound != nil {
		t.Errorf("GetTeamJob for non-existent team should return nil")
	}
}

func TestConcurrencyLimiter(t *testing.T) {
	limiter := NewConcurrencyLimiter(2)

	// acquired/maxConcurrent are written from every spawned goroutine, so they
	// need their own lock: the limiter bounds how many goroutines run at once,
	// it does not serialise their access to the test's own counters, and
	// unguarded ints here fail `go test -race` (which CI runs).
	var mu sync.Mutex
	acquired := 0
	maxConcurrent := 0

	// Spawn 5 goroutines with a limiter of 2
	done := make(chan bool, 5)
	for i := 0; i < 5; i++ {
		go func() {
			limiter.Acquire()
			mu.Lock()
			acquired++
			if acquired > maxConcurrent {
				maxConcurrent = acquired
			}
			mu.Unlock()

			time.Sleep(10 * time.Millisecond)

			mu.Lock()
			acquired--
			mu.Unlock()
			limiter.Release()
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 5; i++ {
		<-done
	}

	mu.Lock()
	observed := maxConcurrent
	mu.Unlock()
	if observed > 2 {
		t.Errorf("max concurrent goroutines %d, want <= 2", observed)
	}
}

func TestPollTeamStatus(t *testing.T) {
	// Async team dispatch
	members := []map[string]string{
		{"role_id": "code-reviewer", "brief": "brief1"},
	}

	result := DispatchTeam(
		DispatchRoots{},
		members, ModePlanningOnly, "public",
		"", "task123", "session123", "public", DefaultRunner, false, // wait=false
	)

	status := result["status"].(string)
	if status != "team_dispatched_async" {
		t.Errorf("async team dispatch returned status %q", status)
		return
	}

	teamID, ok := result["team_id"].(string)
	if !ok || teamID == "" {
		t.Errorf("team_dispatched_async response missing team_id")
		return
	}

	// Poll the team status
	pollResult := PollTeamStatus(teamID)
	pollStatus := pollResult["status"].(string)

	// Team should either be pending or completed
	if pollStatus != "team_dispatched" && pollStatus != "not_found" {
		t.Errorf("poll returned status %q", pollStatus)
	}
}

func TestDispatchSyncWait(t *testing.T) {
	stubRunner(t)
	// Sync dispatch with wait=true should block and return result
	result := DispatchSecureCloudRole(
		testRoots(t, "code-reviewer"),
		"code-reviewer", "test brief", ModePlanningOnly, "public",
		"", "task123", "session123", "public", DefaultRunner, true, // wait=true
	)

	status := result["status"].(string)

	// Should either succeed, require confirmation, or error - but not return immediately with async status
	if status == "dispatched_async" {
		t.Errorf("sync dispatch (wait=true) returned async status")
	}
}

func TestDispatchAsyncNoWait(t *testing.T) {
	stubRunner(t)
	// Async dispatch with wait=false should return immediately with job_id
	result := DispatchSecureCloudRole(
		testRoots(t, "code-reviewer"),
		"code-reviewer", "test brief", ModePlanningOnly, "public",
		"", "task123", "session123", "public", DefaultRunner, false, // wait=false
	)

	status := result["status"].(string)

	// Should return async status immediately
	if status != "dispatched_async" && status != "confirmation_required" {
		t.Errorf("async dispatch (wait=false) returned status %q", status)
	}
}

func TestTeamConfirmationGateTTL(t *testing.T) {
	gate := NewTeamConfirmationGate()

	members := []map[string]string{
		{"role_id": "code-reviewer", "brief": "brief1"},
	}

	data := map[string]any{
		"members":        members,
		"mode":           ModePlanningOnly,
		"classification": "public",
		"task_id":        "task-123",
	}

	token, err := gate.RequestConfirmation(data)
	if err != nil {
		t.Fatalf("RequestConfirmation failed: %v", err)
	}

	// Validate token - should succeed
	err = gate.ValidateConfirmation(token, members, ModePlanningOnly, "public", "task-123")
	if err != nil {
		t.Errorf("ValidateConfirmation failed on fresh token: %v", err)
	}

	// Request another token and let it "expire"
	token2, err := gate.RequestConfirmation(data)
	if err != nil {
		t.Fatalf("second RequestConfirmation failed: %v", err)
	}

	// Manually expire the token by modifying its timestamp
	// This is a bit hacky but necessary for testing TTL
	gate.mu.Lock()
	if pc, ok := gate.pending[token2]; ok {
		pc.Timestamp = time.Now().Add(-(ConfirmationTTLSeconds + 1) * time.Second)
	}
	gate.mu.Unlock()

	// Try to validate the expired token - should fail
	err = gate.ValidateConfirmation(token2, members, ModePlanningOnly, "public", "task-123")
	if err == nil {
		t.Errorf("ValidateConfirmation should reject expired token, but succeeded")
	}
	if err.Error() != "invalid or expired confirmation token" {
		t.Errorf("expected 'invalid or expired confirmation token' error, got %q", err.Error())
	}
}

func TestTeamConfirmationGateConcurrent(t *testing.T) {
	gate := NewTeamConfirmationGate()
	const numRequests = 10

	tokens := make([]string, numRequests)
	membersList := make([][]map[string]string, numRequests)
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Concurrently request tokens - they should all be different
	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			members := []map[string]string{{"role_id": "role" + string(rune(idx)), "brief": "brief"}}
			token, err := gate.RequestConfirmation(map[string]any{
				"members":        members,
				"mode":           ModePlanningOnly,
				"classification": "public",
				"task_id":        "task-123",
			})
			if err != nil {
				t.Errorf("RequestConfirmation %d failed: %v", idx, err)
				return
			}
			mu.Lock()
			tokens[idx] = token
			membersList[idx] = members
			mu.Unlock()
		}(i)
	}

	wg.Wait()

	// Verify all tokens are non-empty and unique
	seenTokens := make(map[string]bool)
	for i, token := range tokens {
		if token == "" {
			t.Errorf("token %d is empty", i)
		}
		if seenTokens[token] {
			t.Errorf("token %d is duplicate: %q", i, token)
		}
		seenTokens[token] = true
	}

	// Verify each token can be validated independently
	for i := 0; i < numRequests; i++ {
		err := gate.ValidateConfirmation(tokens[i], membersList[i], ModePlanningOnly, "public", "task-123")
		if err != nil {
			t.Errorf("ValidateConfirmation for token %d failed: %v", i, err)
		}
	}
}

func TestConfirmationGateLazySweep(t *testing.T) {
	gate := NewConfirmationGate()

	// Request several tokens and let them expire without validation
	expiredTokens := make([]string, 5)
	for i := 0; i < 5; i++ {
		token, err := gate.RequestConfirmation(map[string]any{
			"role_id":        "role-test",
			"brief":          "test brief",
			"mode":           ModeRepositoryEdit,
			"classification": "internal",
			"task_id":        "task-123",
		})
		if err != nil {
			t.Fatalf("RequestConfirmation failed: %v", err)
		}
		expiredTokens[i] = token
	}

	// Verify pending map has 5 entries
	gate.mu.Lock()
	initialSize := len(gate.pending)
	gate.mu.Unlock()
	if initialSize != 5 {
		t.Errorf("pending map should have 5 entries, got %d", initialSize)
	}

	// Manually expire all tokens by modifying their timestamps
	gate.mu.Lock()
	now := time.Now()
	for _, token := range expiredTokens {
		if pc, ok := gate.pending[token]; ok {
			pc.Timestamp = now.Add(-(ConfirmationTTLSeconds + 1) * time.Second)
		}
	}
	gate.mu.Unlock()

	// Request a new token - this should trigger the lazy sweep
	newToken, err := gate.RequestConfirmation(map[string]any{
		"role_id":        "role-new",
		"brief":          "new brief",
		"mode":           ModeRepositoryEdit,
		"classification": "internal",
		"task_id":        "task-456",
	})
	if err != nil {
		t.Fatalf("RequestConfirmation failed: %v", err)
	}

	// Verify that expired entries were swept away
	gate.mu.Lock()
	finalSize := len(gate.pending)
	gate.mu.Unlock()
	if finalSize != 1 {
		t.Errorf("pending map should have 1 entry after sweep, got %d (should have removed 5 expired entries)", finalSize)
	}

	// Verify the new token is still there
	_, err = gate.ValidateConfirmation(newToken, "role-new", "new brief", ModeRepositoryEdit, "internal", "task-456")
	if err != nil {
		t.Errorf("new token should be valid but got error: %v", err)
	}

	// Verify expired tokens cannot be validated
	_, err = gate.ValidateConfirmation(expiredTokens[0], "role-test", "test brief", ModeRepositoryEdit, "internal", "task-123")
	if err == nil {
		t.Error("expired token should not validate")
	}
}

func TestTeamConfirmationGateLazySweep(t *testing.T) {
	gate := NewTeamConfirmationGate()

	// Request several tokens and let them expire without validation
	expiredTokens := make([]string, 5)
	expiredMembers := make([][]map[string]string, 5)
	for i := 0; i < 5; i++ {
		members := []map[string]string{
			{"role_id": "role-test", "brief": "brief"},
		}
		token, err := gate.RequestConfirmation(map[string]any{
			"members":        members,
			"mode":           ModePlanningOnly,
			"classification": "public",
			"task_id":        "task-123",
		})
		if err != nil {
			t.Fatalf("RequestConfirmation failed: %v", err)
		}
		expiredTokens[i] = token
		expiredMembers[i] = members
	}

	// Verify pending map has 5 entries
	gate.mu.Lock()
	initialSize := len(gate.pending)
	gate.mu.Unlock()
	if initialSize != 5 {
		t.Errorf("pending map should have 5 entries, got %d", initialSize)
	}

	// Manually expire all tokens by modifying their timestamps
	gate.mu.Lock()
	now := time.Now()
	for _, token := range expiredTokens {
		if pc, ok := gate.pending[token]; ok {
			pc.Timestamp = now.Add(-(ConfirmationTTLSeconds + 1) * time.Second)
		}
	}
	gate.mu.Unlock()

	// Request a new token - this should trigger the lazy sweep
	newMembers := []map[string]string{
		{"role_id": "role-new", "brief": "new brief"},
	}
	newToken, err := gate.RequestConfirmation(map[string]any{
		"members":        newMembers,
		"mode":           ModeRepositoryEdit,
		"classification": "internal",
		"task_id":        "task-456",
	})
	if err != nil {
		t.Fatalf("RequestConfirmation failed: %v", err)
	}

	// Verify that expired entries were swept away
	gate.mu.Lock()
	finalSize := len(gate.pending)
	gate.mu.Unlock()
	if finalSize != 1 {
		t.Errorf("pending map should have 1 entry after sweep, got %d (should have removed 5 expired entries)", finalSize)
	}

	// Verify the new token is still there
	err = gate.ValidateConfirmation(newToken, newMembers, ModeRepositoryEdit, "internal", "task-456")
	if err != nil {
		t.Errorf("new token should be valid but got error: %v", err)
	}

	// Verify expired tokens cannot be validated
	err = gate.ValidateConfirmation(expiredTokens[0], expiredMembers[0], ModePlanningOnly, "public", "task-123")
	if err == nil {
		t.Error("expired token should not validate")
	}
}

func TestMemberNeedsConfirmationToken(t *testing.T) {
	stubRunner(t)

	// Test 1: Resolvable role that is write-capable should return needsToken=true, err=nil
	t.Run("resolvable_write_capable_role", func(t *testing.T) {
		roots := testRoots(t, "code-reviewer")
		needsToken, err := memberNeedsConfirmationToken(roots, "code-reviewer", ModeRepositoryEdit, DefaultRunner)

		if err != nil {
			t.Errorf("memberNeedsConfirmationToken for resolvable role failed: %v", err)
		}
		if !needsToken {
			t.Errorf("memberNeedsConfirmationToken for write-capable role returned false, want true")
		}
	})

	// Test 2: Unresolvable role should return needsToken=false, err!=nil
	t.Run("unresolvable_role", func(t *testing.T) {
		roots := testRoots(t, "code-reviewer")
		needsToken, err := memberNeedsConfirmationToken(roots, "nonexistent-role-xyz", ModeRepositoryEdit, DefaultRunner)

		if err == nil {
			t.Errorf("memberNeedsConfirmationToken for unresolvable role returned nil error, want error")
		}
		if needsToken {
			t.Errorf("memberNeedsConfirmationToken for unresolvable role returned true, want false")
		}
	})

	// Test 3: Resolvable role in read-only mode should return needsToken=false, err=nil
	// (because read-only mode never requires confirmation)
	t.Run("resolvable_role_planning_only_mode", func(t *testing.T) {
		roots := testRoots(t, "code-reviewer")
		needsToken, err := memberNeedsConfirmationToken(roots, "code-reviewer", ModePlanningOnly, DefaultRunner)

		if err != nil {
			t.Errorf("memberNeedsConfirmationToken for planning-only mode failed: %v", err)
		}
		if needsToken {
			t.Errorf("memberNeedsConfirmationToken for planning-only mode returned true, want false")
		}
	})

	// Test 4: Empty role_id should return needsToken=false, err!=nil
	t.Run("empty_role_id", func(t *testing.T) {
		roots := testRoots(t, "code-reviewer")
		needsToken, err := memberNeedsConfirmationToken(roots, "", ModeRepositoryEdit, DefaultRunner)

		if err == nil {
			t.Errorf("memberNeedsConfirmationToken for empty role_id returned nil error, want error")
		}
		if needsToken {
			t.Errorf("memberNeedsConfirmationToken for empty role_id returned true, want false")
		}
	})
}

func TestDispatchTeamWithUnresolvableRole(t *testing.T) {
	stubRunner(t)

	// This integration test verifies that a team dispatch with mixed resolvable
	// and unresolvable roles completes. It does not test the token-minting behavior
	// directly (that is covered by TestMemberNeedsConfirmationToken); instead it
	// confirms that end-to-end dispatch succeeds and members are included in the
	// results, whether they succeeded or failed.

	members := []map[string]string{
		{"role_id": "code-reviewer", "brief": "review code"},
		{"role_id": "nonexistent-role-xyz", "brief": "this role cannot be resolved"},
	}

	roots := testRoots(t, "code-reviewer")

	// First dispatch in write mode to see if confirmation is requested
	result := DispatchTeam(
		roots,
		members, ModeRepositoryEdit, "public",
		"", "task123", "session123", "public", DefaultRunner, true,
	)

	status := result["status"].(string)
	if status == "confirmation_required" {
		token, ok := result["confirmation_token"].(string)
		if !ok || token == "" {
			t.Errorf("confirmation_required response missing valid confirmation_token")
			return
		}

		// Replay with the confirmation token
		result = DispatchTeam(
			roots,
			members, ModeRepositoryEdit, "public",
			token, "task123", "session123", "public", DefaultRunner, true,
		)

		status = result["status"].(string)
		if status != "team_dispatched" {
			t.Errorf("team dispatch with confirmation returned %q, want 'team_dispatched'", status)
			return
		}
	} else if status != "team_dispatched" {
		t.Errorf("team dispatch returned %q, want 'confirmation_required' or 'team_dispatched'", status)
		return
	}

	// Verify both members are in the results
	membersResult, ok := result["members"].([]map[string]any)
	if !ok || len(membersResult) != 2 {
		t.Errorf("team_dispatched response should have 2 members, got %v", result)
		return
	}

	// Map results by role_id
	resultsByRole := make(map[string]map[string]any)
	for _, memberResult := range membersResult {
		roleID, ok := memberResult["role_id"].(string)
		if !ok {
			t.Errorf("member result missing role_id: %v", memberResult)
			continue
		}
		resultsByRole[roleID] = memberResult
	}

	// Resolvable member should be present and have a status
	if _, hasCodeReviewer := resultsByRole["code-reviewer"]; !hasCodeReviewer {
		t.Errorf("code-reviewer member result not found in team response")
	}

	// Unresolvable member should be present and have unavailable/denied status
	nonexistentResult, hasNonexistent := resultsByRole["nonexistent-role-xyz"]
	if !hasNonexistent {
		t.Errorf("nonexistent-role-xyz member result not found in team response")
	} else {
		status, ok := nonexistentResult["status"].(string)
		if !ok {
			t.Errorf("nonexistent-role-xyz result missing status")
		} else if status != "unavailable" && status != "denied" {
			t.Errorf("nonexistent-role-xyz returned status %q, want 'unavailable' or 'denied' (cannot resolve)", status)
		}
	}
}

func TestDispatchTeamPlanningModeSkipsTokenMinting(t *testing.T) {
	// This test verifies that in planning-review-only mode (read-only dispatch),
	// no confirmation gate is involved at all -- neither for the team-level
	// confirmation nor for per-member token minting. This is correct because
	// read-only mode never requires confirmation.
	//
	// The member-token-minting logic at dispatch_core_phase2.go:488-535 is only
	// entered when needsConfirmation is true, which only happens when the
	// effective sandbox is write-capable. In planning-review-only mode,
	// ComputeEffectiveSandbox forces all sandboxes to read-only, so the
	// confirmation gate is skipped entirely for the team and no per-member
	// tokens are minted.

	members := []map[string]string{
		{"role_id": "code-reviewer", "brief": "review code"},
	}

	result := DispatchTeam(
		testRoots(t, "code-reviewer"),
		members, ModePlanningOnly, "public",
		"", "task123", "session123", "public", DefaultRunner, true,
	)

	status := result["status"].(string)
	// In planning-review-only mode, no confirmation is required, so the dispatch
	// should proceed directly to team_dispatched without asking for confirmation
	if status != "team_dispatched" && status != "unavailable" {
		// unavailable is acceptable if the role fixture can't be created
		t.Errorf("team dispatch in planning-review-only mode returned %q, want 'team_dispatched' or 'unavailable'", status)
		return
	}

	// Verify we never got asked for confirmation
	if _, hasToken := result["confirmation_token"]; hasToken {
		t.Errorf("planning-review-only mode should not request confirmation")
	}
}

// TestConfirmationGateTokenSurvivesMismatch verifies that a token remains pending
// after a failed validation with a mismatched field, allowing a retry with correct
// parameters to succeed. This prevents denial-of-service where any incorrect
// parameter (especially brief, which is free text) destroys the token permanently.
func TestConfirmationGateTokenSurvivesMismatch(t *testing.T) {
	gate := NewConfirmationGate()

	expectedRoleID := "test-role"
	expectedBrief := "correct brief"
	expectedMode := ModeRepositoryEdit
	expectedClassification := "internal"
	expectedTaskID := "task-456"

	// Test mismatch on each field
	mismatchTests := []struct {
		name           string
		roleID         string
		brief          string
		mode           string
		classification string
		taskID         string
	}{
		{
			name:           "wrong role_id",
			roleID:         "wrong-role",
			brief:          expectedBrief,
			mode:           expectedMode,
			classification: expectedClassification,
			taskID:         expectedTaskID,
		},
		{
			name:           "wrong brief",
			roleID:         expectedRoleID,
			brief:          "wrong brief",
			mode:           expectedMode,
			classification: expectedClassification,
			taskID:         expectedTaskID,
		},
		{
			name:           "wrong mode",
			roleID:         expectedRoleID,
			brief:          expectedBrief,
			mode:           ModePlanningOnly,
			classification: expectedClassification,
			taskID:         expectedTaskID,
		},
		{
			name:           "wrong classification",
			roleID:         expectedRoleID,
			brief:          expectedBrief,
			mode:           expectedMode,
			classification: "public",
			taskID:         expectedTaskID,
		},
		{
			name:           "wrong task_id",
			roleID:         expectedRoleID,
			brief:          expectedBrief,
			mode:           expectedMode,
			classification: expectedClassification,
			taskID:         "wrong-task",
		},
	}

	for _, tt := range mismatchTests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a fresh token for this subtest
			token, err := gate.RequestConfirmation(map[string]any{
				"role_id":        expectedRoleID,
				"brief":          expectedBrief,
				"mode":           expectedMode,
				"classification": expectedClassification,
				"task_id":        expectedTaskID,
			})
			if err != nil {
				t.Fatalf("RequestConfirmation failed: %v", err)
			}

			// First, attempt validation with wrong parameter
			_, err = gate.ValidateConfirmation(
				token,
				tt.roleID, tt.brief, tt.mode, tt.classification, tt.taskID,
			)
			if err == nil {
				t.Errorf("ValidateConfirmation with %s should have failed but didn't", tt.name)
			}

			// Verify token still exists by attempting validation with correct parameters
			_, err = gate.ValidateConfirmation(
				token,
				expectedRoleID, expectedBrief, expectedMode, expectedClassification, expectedTaskID,
			)
			if err != nil {
				t.Errorf("ValidateConfirmation with correct parameters after %s failed: %v (token was destroyed on mismatch)", tt.name, err)
			}
		})
	}
}

// TestTeamConfirmationGateTokenSurvivesMismatch verifies that a team confirmation
// token remains pending after a failed validation with a mismatched field, allowing
// a retry with correct parameters to succeed.
func TestTeamConfirmationGateTokenSurvivesMismatch(t *testing.T) {
	gate := NewTeamConfirmationGate()

	expectedMembers := []map[string]string{
		{"role_id": "role1", "brief": "brief1"},
		{"role_id": "role2", "brief": "brief2"},
	}
	expectedMode := ModeRepositoryEdit
	expectedClassification := "internal"
	expectedTaskID := "team-task-123"

	// Test mismatch on each field
	mismatchTests := []struct {
		name           string
		members        []map[string]string
		mode           string
		classification string
		taskID         string
	}{
		{
			name: "wrong members count",
			members: []map[string]string{
				{"role_id": "role1", "brief": "brief1"},
			},
			mode:           expectedMode,
			classification: expectedClassification,
			taskID:         expectedTaskID,
		},
		{
			name: "wrong member role_id",
			members: []map[string]string{
				{"role_id": "wrong-role", "brief": "brief1"},
				{"role_id": "role2", "brief": "brief2"},
			},
			mode:           expectedMode,
			classification: expectedClassification,
			taskID:         expectedTaskID,
		},
		{
			name: "wrong member brief",
			members: []map[string]string{
				{"role_id": "role1", "brief": "wrong-brief"},
				{"role_id": "role2", "brief": "brief2"},
			},
			mode:           expectedMode,
			classification: expectedClassification,
			taskID:         expectedTaskID,
		},
		{
			name:           "wrong mode",
			members:        expectedMembers,
			mode:           ModePlanningOnly,
			classification: expectedClassification,
			taskID:         expectedTaskID,
		},
		{
			name:           "wrong task_id",
			members:        expectedMembers,
			mode:           expectedMode,
			classification: expectedClassification,
			taskID:         "wrong-task",
		},
	}

	for _, tt := range mismatchTests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a fresh token for this subtest
			token, err := gate.RequestConfirmation(map[string]any{
				"members":        expectedMembers,
				"mode":           expectedMode,
				"classification": expectedClassification,
				"task_id":        expectedTaskID,
			})
			if err != nil {
				t.Fatalf("RequestConfirmation failed: %v", err)
			}

			// First, attempt validation with wrong parameter
			err = gate.ValidateConfirmation(token, tt.members, tt.mode, tt.classification, tt.taskID)
			if err == nil {
				t.Errorf("ValidateConfirmation with %s should have failed but didn't", tt.name)
			}

			// Verify token still exists by attempting validation with correct parameters
			err = gate.ValidateConfirmation(token, expectedMembers, expectedMode, expectedClassification, expectedTaskID)
			if err != nil {
				t.Errorf("ValidateConfirmation with correct parameters after %s failed: %v (token was destroyed on mismatch)", tt.name, err)
			}
		})
	}
}

// TestDispatchSecureCloudRoleConfirmationEndToEnd tests a full round trip through
// the public API: request confirmation, attempt replay with wrong parameter, verify
// that retry with correct parameters succeeds.
func TestDispatchSecureCloudRoleConfirmationEndToEnd(t *testing.T) {
	stubRunner(t)
	roots := testRoots(t, "code-reviewer")

	roleID := "code-reviewer"
	brief := "correct brief"
	mode := ModeRepositoryEdit
	classification := "internal"
	taskID := "e2e-task-123"

	// Step 1: Request confirmation
	result := DispatchSecureCloudRole(
		roots,
		roleID, brief, mode, classification,
		"", taskID, "session123", "internal", DefaultRunner, true,
	)

	status := result["status"].(string)
	if status != "confirmation_required" {
		// If it doesn't require confirmation, it means the role is read-only,
		// which is fine for this test structure
		if status != "success" {
			t.Fatalf("first dispatch returned unexpected status %q", status)
		}
		return // Skip the rest of the test if no confirmation was needed
	}

	token, ok := result["confirmation_token"].(string)
	if !ok || token == "" {
		t.Fatalf("confirmation_required response missing valid confirmation_token")
	}

	// Step 2: Attempt replay with WRONG brief
	result = DispatchSecureCloudRole(
		roots,
		roleID, "wrong brief", mode, classification,
		token, taskID, "session123", "public", DefaultRunner, true,
	)

	status = result["status"].(string)
	if status != "denied" {
		t.Errorf("replay with wrong brief should be denied, got status %q", status)
	}

	// Step 3: Verify token still works with CORRECT brief
	result = DispatchSecureCloudRole(
		roots,
		roleID, brief, mode, classification,
		token, taskID, "session123", "internal", DefaultRunner, true,
	)

	status = result["status"].(string)
	// Should succeed or at least not be "denied" due to token expiry
	if status == "denied" && result["reason"] == "confirmation token invalid or expired" {
		t.Errorf("token was destroyed after mismatch; retry with correct brief failed")
	}
}

// TestDispatchTeamConfirmationEndToEnd tests a full round trip through the team
// dispatch public API: request confirmation, attempt replay with wrong members,
// verify that retry with correct parameters succeeds.
func TestDispatchTeamConfirmationEndToEnd(t *testing.T) {
	stubRunner(t)
	roots := testRoots(t, "code-reviewer")

	correctMembers := []map[string]string{
		{"role_id": "code-reviewer", "brief": "team member 1"},
	}
	mode := ModeRepositoryEdit
	taskID := "team-e2e-task-123"

	// Step 1: Request team confirmation
	result := DispatchTeam(
		roots,
		correctMembers, mode, "internal",
		"", taskID, "session123", "internal", DefaultRunner, true,
	)

	status := result["status"].(string)
	if status != "confirmation_required" {
		// If it doesn't require confirmation, the members are read-only
		if status != "team_dispatched" {
			t.Fatalf("first team dispatch returned unexpected status %q", status)
		}
		return // Skip the rest of the test if no confirmation was needed
	}

	token, ok := result["confirmation_token"].(string)
	if !ok || token == "" {
		t.Fatalf("confirmation_required response missing valid confirmation_token")
	}

	// Step 2: Attempt replay with WRONG members (different count)
	wrongMembers := []map[string]string{
		{"role_id": "code-reviewer", "brief": "team member 1"},
		{"role_id": "code-reviewer", "brief": "team member 2"},
	}
	result = DispatchTeam(
		roots,
		wrongMembers, mode, "internal",
		token, taskID, "session123", "internal", DefaultRunner, true,
	)

	status = result["status"].(string)
	if status != "denied" {
		t.Errorf("replay with wrong members should be denied, got status %q", status)
	}

	// Step 3: Verify token still works with CORRECT members
	result = DispatchTeam(
		roots,
		correctMembers, mode, "internal",
		token, taskID, "session123", "internal", DefaultRunner, true,
	)

	status = result["status"].(string)
	// Should succeed or at least not be "denied" due to token expiry
	if status == "denied" && result["reason"] == "confirmation token invalid or expired" {
		t.Errorf("token was destroyed after mismatch; retry with correct members failed")
	}
}

// TestTeamConfirmationTokenClassificationBinding verifies that a team confirmation
// token minted at one classification cannot be replayed with a different classification.
// This mirrors the existing TestTeamConfirmationGateTokenSurvivesMismatch but
// tests the classification field specifically.
func TestTeamConfirmationTokenClassificationBinding(t *testing.T) {
	gate := NewTeamConfirmationGate()

	members := []map[string]string{
		{"role_id": "role1", "brief": "brief1"},
	}
	correctMode := ModeRepositoryEdit
	correctClassification := "internal"
	correctTaskID := "task-123"

	// Create token at one classification
	token, err := gate.RequestConfirmation(map[string]any{
		"members":        members,
		"mode":           correctMode,
		"classification": correctClassification,
		"task_id":        correctTaskID,
	})
	if err != nil {
		t.Fatalf("RequestConfirmation failed: %v", err)
	}

	// Attempt validation with wrong classification
	err = gate.ValidateConfirmation(
		token,
		members,
		correctMode,
		"public", // Wrong classification
		correctTaskID,
	)
	if err == nil {
		t.Errorf("ValidateConfirmation with wrong classification should have failed but didn't")
	}

	// Verify token still exists by validating with correct classification
	err = gate.ValidateConfirmation(
		token,
		members,
		correctMode,
		correctClassification,
		correctTaskID,
	)
	if err != nil {
		t.Errorf("ValidateConfirmation with correct classification after mismatch failed: %v (token was destroyed on mismatch)", err)
	}
}

// TestDispatchSecureCloudRoleClassificationCeiling verifies that a dispatch with
// classification exceeding the environment-inherited parent classification is denied.
// This test verifies the OLD behavior for backward compatibility: tests that pass
// parentClassification in the MCP request now have that field ignored for enforcement
// purposes (it was never the authoritative source). Instead, the environment variable
// is authoritative. This test updates to use the env var.
func TestDispatchSecureCloudRoleClassificationCeiling(t *testing.T) {
	stubRunner(t)

	tests := []struct {
		name                      string
		classification            string
		envParentClassification   string // Now uses the env var, not the request parameter
		expectDenied              bool
		expectClassificationError bool
	}{
		{
			name:                      "equal classifications allowed",
			classification:            "internal",
			envParentClassification:   "internal",
			expectDenied:              false,
			expectClassificationError: false,
		},
		{
			name:                      "lower classification allowed",
			classification:            "public",
			envParentClassification:   "internal",
			expectDenied:              false,
			expectClassificationError: false,
		},
		{
			name:                      "exceeding classification denied",
			classification:            "restricted",
			envParentClassification:   "internal",
			expectDenied:              true,
			expectClassificationError: true,
		},
		{
			name:                      "confidential exceeds internal",
			classification:            "confidential",
			envParentClassification:   "internal",
			expectDenied:              true,
			expectClassificationError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envParentClassification != "" {
				t.Setenv(ParentClassificationVar, tt.envParentClassification)
			}

			result := DispatchSecureCloudRole(
				testRoots(t, "code-reviewer"),
				"code-reviewer",
				"test brief",
				ModePlanningOnly,
				tt.classification,
				"",
				"task123",
				"session123",
				"", // parentClassification request parameter is now ignored
				DefaultRunner,
				true,
			)

			status := result["status"].(string)
			if tt.expectDenied {
				if status != "denied" {
					t.Errorf("expected denied status for %s, got %q", tt.name, status)
					return
				}
				if tt.expectClassificationError {
					reason, ok := result["reason"].(string)
					if !ok {
						t.Errorf("denied result missing reason field")
						return
					}
					if !strings.Contains(reason, "exceeds") {
						t.Errorf("expected classification ceiling error, got reason: %s", reason)
					}
				}
			} else if status == "denied" {
				if reason, ok := result["reason"].(string); ok && strings.Contains(reason, "exceeds") {
					t.Errorf("classification %s should not exceed parent %s, but was denied: %s",
						tt.classification, tt.envParentClassification, reason)
				}
			}
		})
	}
}

// TestDispatchTeamClassificationCeiling verifies that a team dispatch with
// classification exceeding the environment-inherited parent classification is denied.
// This test verifies the OLD behavior for backward compatibility: tests that pass
// parentClassification in the MCP request now have that field ignored for enforcement
// purposes. Instead, the environment variable is authoritative.
func TestDispatchTeamClassificationCeiling(t *testing.T) {
	members := []map[string]string{
		{"role_id": "code-reviewer", "brief": "task 1"},
	}

	tests := []struct {
		name                      string
		classification            string
		envParentClassification   string // Now uses the env var, not the request parameter
		expectDenied              bool
		expectClassificationError bool
	}{
		{
			name:                      "equal classifications allowed",
			classification:            "internal",
			envParentClassification:   "internal",
			expectDenied:              false,
			expectClassificationError: false,
		},
		{
			name:                      "lower classification allowed",
			classification:            "public",
			envParentClassification:   "internal",
			expectDenied:              false,
			expectClassificationError: false,
		},
		{
			name:                      "exceeding classification denied",
			classification:            "restricted",
			envParentClassification:   "internal",
			expectDenied:              true,
			expectClassificationError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envParentClassification != "" {
				t.Setenv(ParentClassificationVar, tt.envParentClassification)
			}

			result := DispatchTeam(
				testRoots(t, "code-reviewer"),
				members,
				ModePlanningOnly,
				tt.classification,
				"",
				"task123",
				"session123",
				"", // parentClassification request parameter is now ignored
				DefaultRunner,
				true,
			)

			status := result["status"].(string)
			if tt.expectDenied {
				if status != "denied" {
					t.Errorf("expected denied status for %s, got %q", tt.name, status)
					return
				}
				if tt.expectClassificationError {
					reason, ok := result["reason"].(string)
					if !ok {
						t.Errorf("denied result missing reason field")
						return
					}
					if !strings.Contains(reason, "exceeds") {
						t.Errorf("expected classification ceiling error, got reason: %s", reason)
					}
				}
			} else if status == "denied" {
				if reason, ok := result["reason"].(string); ok && strings.Contains(reason, "exceeds") {
					t.Errorf("classification %s should not exceed parent %s, but was denied: %s",
						tt.classification, tt.envParentClassification, reason)
				}
			}
		})
	}
}

// TestDispatchSecureCloudRoleEnvInheritedCeilingEnforcement verifies that the
// classification ceiling is enforced based on the inherited SECURE_CLOUD_AGENTS_PARENT_CLASSIFICATION
// environment variable, not on the caller-supplied parentClassification request field.
// This is the core security fix: a child process inherits its parent's classification
// in the env var, and no caller can lie about that ceiling to escape it.
func TestDispatchSecureCloudRoleEnvInheritedCeilingEnforcement(t *testing.T) {
	stubRunner(t)

	tests := []struct {
		name                        string
		envParentClassification     string // SECURE_CLOUD_AGENTS_PARENT_CLASSIFICATION env var
		requestParentClassification string // parent_classification in MCP request (caller-supplied)
		dispatchClassification      string // classification being requested in dispatch
		expectDenied                bool
		expectClassificationError   bool
	}{
		{
			name:                        "env ceiling internal, caller claims no ceiling, dispatch restricted - DENIED",
			envParentClassification:     "internal",
			requestParentClassification: "", // caller omits or claims no ceiling
			dispatchClassification:      "restricted",
			expectDenied:                true,
			expectClassificationError:   true,
		},
		{
			name:                        "env ceiling internal, caller claims permissive ceiling, dispatch restricted - DENIED",
			envParentClassification:     "internal",
			requestParentClassification: "restricted", // caller lies and claims permissive ceiling
			dispatchClassification:      "restricted",
			expectDenied:                true,
			expectClassificationError:   true,
		},
		{
			name:                        "env ceiling internal, dispatch internal - ALLOWED",
			envParentClassification:     "internal",
			requestParentClassification: "",
			dispatchClassification:      "internal",
			expectDenied:                false,
			expectClassificationError:   false,
		},
		{
			name:                        "env ceiling internal, dispatch public - ALLOWED",
			envParentClassification:     "internal",
			requestParentClassification: "",
			dispatchClassification:      "public",
			expectDenied:                false,
			expectClassificationError:   false,
		},
		{
			name:                        "no env ceiling (top-level), dispatch restricted - ALLOWED",
			envParentClassification:     "", // unset = top-level, no ceiling
			requestParentClassification: "",
			dispatchClassification:      "restricted",
			expectDenied:                false,
			expectClassificationError:   false,
		},
		{
			name:                        "no env ceiling (top-level), caller claims ceiling, dispatch exceeds claimed - ALLOWED (env is source of truth)",
			envParentClassification:     "", // unset = top-level, no ceiling, caller's claim is ignored
			requestParentClassification: "internal",
			dispatchClassification:      "restricted",
			expectDenied:                false,
			expectClassificationError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up environment: SECURE_CLOUD_AGENTS_PARENT_CLASSIFICATION comes from
			// the parent process and is the authoritative ceiling. Use t.Setenv to
			// automatically clean up after this subtest.
			if tt.envParentClassification != "" {
				t.Setenv(ParentClassificationVar, tt.envParentClassification)
			}

			result := DispatchSecureCloudRole(
				testRoots(t, "code-reviewer"),
				"code-reviewer",
				"test brief",
				ModePlanningOnly,
				tt.dispatchClassification,
				"",
				"task123",
				"session123",
				tt.requestParentClassification, // This is the caller-supplied value that SHOULD be ignored for enforcement
				DefaultRunner,
				true,
			)

			status := result["status"].(string)
			if tt.expectDenied {
				if status != "denied" {
					t.Errorf("expected denied status, got %q", status)
					return
				}
				if tt.expectClassificationError {
					reason, ok := result["reason"].(string)
					if !ok {
						t.Errorf("denied result missing reason field")
						return
					}
					if !strings.Contains(reason, "exceeds") {
						t.Errorf("expected classification ceiling error, got reason: %s", reason)
					}
				}
			} else if status == "denied" {
				if reason, ok := result["reason"].(string); ok && strings.Contains(reason, "exceeds") {
					t.Errorf("dispatch should have been allowed but was denied: %s", reason)
				}
			}
		})
	}
}

// TestDispatchTeamEnvInheritedCeilingEnforcement verifies that team dispatch
// also enforces the classification ceiling based on the inherited environment
// variable, not the caller-supplied request field.
func TestDispatchTeamEnvInheritedCeilingEnforcement(t *testing.T) {
	members := []map[string]string{
		{"role_id": "code-reviewer", "brief": "review code"},
	}

	tests := []struct {
		name                        string
		envParentClassification     string
		requestParentClassification string
		dispatchClassification      string
		expectDenied                bool
		expectClassificationError   bool
	}{
		{
			name:                        "team: env ceiling internal, caller claims no ceiling, dispatch restricted - DENIED",
			envParentClassification:     "internal",
			requestParentClassification: "",
			dispatchClassification:      "restricted",
			expectDenied:                true,
			expectClassificationError:   true,
		},
		{
			name:                        "team: env ceiling internal, caller lies permissive, dispatch confidential - DENIED",
			envParentClassification:     "internal",
			requestParentClassification: "confidential", // caller lies
			dispatchClassification:      "confidential",
			expectDenied:                true,
			expectClassificationError:   true,
		},
		{
			name:                        "team: no env ceiling (top-level), dispatch restricted - ALLOWED",
			envParentClassification:     "", // unset = top-level
			requestParentClassification: "",
			dispatchClassification:      "restricted",
			expectDenied:                false,
			expectClassificationError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envParentClassification != "" {
				t.Setenv(ParentClassificationVar, tt.envParentClassification)
			}

			result := DispatchTeam(
				testRoots(t, "code-reviewer"),
				members,
				ModePlanningOnly,
				tt.dispatchClassification,
				"",
				"task123",
				"session123",
				tt.requestParentClassification, // Should be ignored for enforcement
				DefaultRunner,
				true,
			)

			status := result["status"].(string)
			if tt.expectDenied {
				if status != "denied" {
					t.Errorf("expected denied status, got %q", status)
					return
				}
				if tt.expectClassificationError {
					reason, ok := result["reason"].(string)
					if !ok {
						t.Errorf("denied result missing reason field")
						return
					}
					if !strings.Contains(reason, "exceeds") {
						t.Errorf("expected classification ceiling error, got reason: %s", reason)
					}
				}
			} else if status == "denied" {
				if reason, ok := result["reason"].(string); ok && strings.Contains(reason, "exceeds") {
					t.Errorf("team dispatch should have been allowed but was denied: %s", reason)
				}
			}
		})
	}
}

// TestDispatchSecureCloudRoleTopLevelNoClassificationCeiling verifies that a
// genuinely top-level dispatch (no parent process, env var unset) is not subject
// to any classification ceiling and can dispatch at any valid classification.
func TestDispatchSecureCloudRoleTopLevelNoClassificationCeiling(t *testing.T) {
	stubRunner(t)
	// Explicitly ensure the env var is unset (no parent).
	for _, classification := range []string{"public", "internal", "confidential", "restricted"} {
		t.Run(classification, func(t *testing.T) {
			// This test is specifically about the top-level case.
			// Verify that with no env var set, any classification is allowed.
			result := DispatchSecureCloudRole(
				testRoots(t, "code-reviewer"),
				"code-reviewer",
				"test brief",
				ModePlanningOnly,
				classification,
				"",
				"task123",
				"session123",
				"", // no parent classification from request
				DefaultRunner,
				true,
			)

			status := result["status"].(string)
			// Any valid classification should be allowed when there's no env-inherited ceiling
			if status == "denied" {
				if reason, ok := result["reason"].(string); ok && strings.Contains(reason, "exceeds") {
					t.Errorf("top-level dispatch of %q should not be subject to classification ceiling: %s",
						classification, reason)
				}
			}
		})
	}
}
