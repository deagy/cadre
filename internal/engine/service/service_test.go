package service

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/deagy/cadre/cli/internal/engine/agents"
	"github.com/deagy/cadre/cli/internal/engine/executor"
	"github.com/deagy/cadre/cli/internal/engine/runtime"
)

func testServer(t *testing.T) (http.Handler, string) {
	t.Helper()
	working, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	kernelRoot := filepath.Dir(filepath.Dir(filepath.Dir(working)))

	// One store for the whole test, so a resume finds what a create left.
	store := executor.NewMemoryCheckpointer()
	server := &Server{
		KernelRoot: kernelRoot,
		Build: func(request runtime.PlanRequest) (runtime.PlanRequest, error) {
			request.Client = agents.FakeModelClient{}
			request.Checkpointer = store
			return request, nil
		},
	}
	return server.Handler(), t.TempDir()
}

func post(t *testing.T, handler http.Handler, path string, body any) (int, map[string]any) {
	t.Helper()
	encoded, _ := json.Marshal(body)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, bytes.NewReader(encoded)))
	var payload map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &payload)
	return recorder.Code, payload
}

func get(t *testing.T, handler http.Handler, path string) (int, map[string]any) {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	var payload map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &payload)
	return recorder.Code, payload
}

// The three routes describe a run the same way the CLI does.
func TestCreateResumeAndStatus(t *testing.T) {
	handler, root := testServer(t)

	code, payload := post(t, handler, "/tasks", CreateTaskRequest{
		TaskID: "task-1", Task: "refactor the architecture of the billing service", Root: root,
	})
	if code != http.StatusOK {
		t.Fatalf("POST /tasks = %d: %v", code, payload)
	}
	if payload["status"] != "interrupted" {
		t.Fatalf("status = %v, want the run to stop for an approval", payload["status"])
	}
	if payload["interrupt"] == nil {
		t.Error("an interrupted run reported no interrupt payload")
	}

	// Status must not advance the run.
	code, status := get(t, handler, "/tasks/task-1?root="+root)
	if code != http.StatusOK {
		t.Fatalf("GET /tasks/task-1 = %d: %v", code, status)
	}
	if status["status"] != "interrupted" {
		t.Errorf("status = %v, want the run still waiting", status["status"])
	}

	code, resumed := post(t, handler, "/tasks/task-1/resume", ResumeRequest{
		Root: root,
		Decision: map[string]any{
			"status":   "approved",
			"approver": map[string]any{"id": "product_owner", "role": "Product Owner", "kind": "human"},
			"evidence_refs": []any{map[string]any{
				"evidence_id": "e1", "uri": "https://example/1", "hash_algorithm": "sha256",
				"hash": "abc", "classification": "internal",
			}},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("resume = %d: %v", code, resumed)
	}
	if resumed["status"] == nil {
		t.Error("resume returned no status")
	}
}

// Re-creating a planned task reports that, rather than re-running it.
//
// Re-invoking would dispatch agents again for gates already decided.
func TestRecreatingAPlannedTaskDoesNotRerunIt(t *testing.T) {
	handler, root := testServer(t)
	body := CreateTaskRequest{TaskID: "task-1", Task: "refactor the architecture", Root: root}

	if code, payload := post(t, handler, "/tasks", body); code != http.StatusOK {
		t.Fatalf("first create = %d: %v", code, payload)
	}
	code, payload := post(t, handler, "/tasks", body)
	if code != http.StatusOK {
		t.Fatalf("second create = %d: %v", code, payload)
	}
	if payload["status"] != "already-planned" {
		t.Errorf("status = %v, want already-planned", payload["status"])
	}
	if payload["gate_sequence"] == nil {
		t.Error("already-planned reported no gate sequence")
	}
}

// A configuration conflict is 409: the request was well-formed and the server
// is healthy; the task was simply planned differently.
func TestReplanningWithDifferentTextIsAConflict(t *testing.T) {
	handler, root := testServer(t)

	if code, _ := post(t, handler, "/tasks", CreateTaskRequest{
		TaskID: "task-1", Task: "refactor the architecture", Root: root}); code != http.StatusOK {
		t.Fatalf("first create = %d", code)
	}

	code, payload := post(t, handler, "/tasks", CreateTaskRequest{
		TaskID: "task-1", Task: "something else entirely", Root: root})
	if code != http.StatusConflict {
		t.Errorf("re-plan with different text = %d, want 409: %v", code, payload)
	}
}

// A bad GitLab reference is rejected before anything is planned, so a task is
// never recorded with a source id that turned out not to exist.
func TestABadIssueReferenceIsRejectedBeforePlanning(t *testing.T) {
	handler, root := testServer(t)

	code, payload := post(t, handler, "/tasks", CreateTaskRequest{
		TaskID: "task-1", Task: "do the thing", Root: root,
		IntentGitLabIssue: "not-a-reference",
	})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("bad issue reference = %d, want 422: %v", code, payload)
	}
	if runtime.TaskExists(root, "task-1") {
		t.Error("the task was planned despite the bad reference")
	}
}

func TestMissingTasksAndFieldsAreReported(t *testing.T) {
	handler, root := testServer(t)

	if code, _ := post(t, handler, "/tasks", CreateTaskRequest{Task: "x", Root: root}); code != http.StatusUnprocessableEntity {
		t.Errorf("create with no task_id = %d, want 422", code)
	}
	if code, _ := post(t, handler, "/tasks/nope/resume", ResumeRequest{Root: root}); code != http.StatusNotFound {
		t.Errorf("resume of an unplanned task = %d, want 404", code)
	}
	if code, _ := get(t, handler, "/tasks/nope?root="+root); code != http.StatusNotFound {
		t.Errorf("status of an unplanned task = %d, want 404", code)
	}
	if code, _ := get(t, handler, "/tasks/nope"); code != http.StatusUnprocessableEntity {
		t.Errorf("status with no root = %d, want 422", code)
	}
}

// Asking what a run is waiting for must never move it.
//
// The observable case is a task planned but not yet started: if status
// advanced the run it would dispatch that task's agents, so a read-only
// question would spend model calls and change what the next answer is.
func TestStatusDoesNotStartAPlannedTask(t *testing.T) {
	working, _ := os.Getwd()
	kernelRoot := filepath.Dir(filepath.Dir(filepath.Dir(working)))
	root := t.TempDir()
	store := executor.NewMemoryCheckpointer()

	prepare := func(request runtime.PlanRequest) (runtime.PlanRequest, error) {
		request.Client = agents.FakeModelClient{}
		request.Checkpointer = store
		return request, nil
	}
	server := &Server{KernelRoot: kernelRoot, Build: prepare}
	handler := server.Handler()

	// Plan without starting: write the config the way ExecutorForTask does,
	// then ask for status.
	request, _ := prepare(runtime.PlanRequest{
		Root: root, KernelRoot: kernelRoot, TaskID: "task-1",
		TaskText: "refactor the architecture of the billing service",
	})
	if _, _, err := runtime.ExecutorForTask(request); err != nil {
		t.Fatalf("planning: %v", err)
	}

	code, payload := get(t, handler, "/tasks/task-1?root="+root)
	if code != http.StatusOK {
		t.Fatalf("status = %d: %v", code, payload)
	}
	if payload["status"] != "planned" {
		t.Errorf("status = %v, want planned -- asking must not start the run", payload["status"])
	}

	if _, found, err := store.Load("task-1"); err != nil || found {
		t.Error("asking for status created a checkpoint; the run was started by a read")
	}
}
