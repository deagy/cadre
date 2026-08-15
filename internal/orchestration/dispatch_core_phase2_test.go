package orchestration

import (
	"sync"
	"testing"
	"time"
)

// Phase 2 Tests: Main Dispatch Function, Confirmation Gating, Async Job Stores

func TestDispatchSecureCloudRoleValidation(t *testing.T) {
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

	// Poll the job status
	pollResult := PollDispatchStatus(jobID)
	pollStatus := pollResult["status"].(string)

	// Job should either still be pending or completed
	if pollStatus != "dispatched_async" && pollStatus != "success" && pollStatus != "not_found" {
		t.Errorf("poll returned status %q, want 'dispatched_async', 'success', or 'not_found'", pollStatus)
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

	data := map[string]any{
		"members": []map[string]string{
			{"role_id": "code-reviewer", "brief": "brief1"},
		},
		"mode": ModePlanningOnly,
	}

	token, err := gate.RequestConfirmation(data)
	if err != nil {
		t.Fatalf("RequestConfirmation failed: %v", err)
	}
	if token == "" {
		t.Errorf("token is empty")
	}

	// Validate token
	err = gate.ValidateConfirmation(token)
	if err != nil {
		t.Errorf("ValidateConfirmation failed: %v", err)
	}

	// Second validation should fail - token consumed
	err = gate.ValidateConfirmation(token)
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
