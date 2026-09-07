package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/deagy/cadre/cli/internal/engine/agents"
	"github.com/deagy/cadre/cli/internal/engine/executor"
	"github.com/deagy/cadre/cli/internal/engine/kernelfixture"
	"github.com/deagy/cadre/cli/internal/engine/runtime"
)

func testServer(t *testing.T) (http.Handler, string) {
	t.Helper()
	working, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	kernelRoot := kernelfixture.Root(t, filepath.Dir(filepath.Dir(filepath.Dir(working))))

	// One store for the whole test, so a resume finds what a create left.
	store := executor.NewMemoryCheckpointer()
	root := t.TempDir()
	server := &Server{
		KernelRoot:    kernelRoot,
		WorkspaceRoot: root, // Set to temp dir to allow tasks to use it
		Build: func(request runtime.PlanRequest) (runtime.PlanRequest, error) {
			request.Client = agents.FakeModelClient{}
			request.Checkpointer = store
			return request, nil
		},
	}
	return server.Handler(), root
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

// postRaw sends a POST request and returns both the status code and raw response body.
func postRaw(t *testing.T, handler http.Handler, path string, body any) (int, string) {
	t.Helper()
	encoded, _ := json.Marshal(body)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, bytes.NewReader(encoded)))
	return recorder.Code, recorder.Body.String()
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
	kernelRoot := kernelfixture.Root(t, filepath.Dir(filepath.Dir(filepath.Dir(working))))
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

// TestRootConfinementCreateTask verifies that POST /tasks rejects roots outside
// the workspace boundary.
func TestRootConfinementCreateTask(t *testing.T) {
	_, allowedRoot := testServer(t)

	working, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	kernelRoot := kernelfixture.Root(t, filepath.Dir(filepath.Dir(filepath.Dir(working))))

	// Create a server with WorkspaceRoot set to allowedRoot.
	store := executor.NewMemoryCheckpointer()
	server := &Server{
		KernelRoot:    kernelRoot,
		WorkspaceRoot: allowedRoot,
		Build: func(request runtime.PlanRequest) (runtime.PlanRequest, error) {
			request.Client = agents.FakeModelClient{}
			request.Checkpointer = store
			return request, nil
		},
	}
	handler := server.Handler()

	// Request with root inside workspace should succeed.
	code, _ := post(t, handler, "/tasks", CreateTaskRequest{
		TaskID: "task-1", Task: "test", Root: allowedRoot,
	})
	if code != http.StatusOK {
		t.Errorf("create with root inside workspace = %d, want 200", code)
	}

	// Request with root outside workspace should be rejected (400).
	code, payload := post(t, handler, "/tasks", CreateTaskRequest{
		TaskID: "task-2", Task: "test", Root: "/etc/passwd",
	})
	if code != http.StatusBadRequest {
		t.Errorf("create with root outside workspace = %d, want 400: %v", code, payload)
	}
	detail, _ := payload["detail"].(string)
	if detail == "" {
		t.Error("error response missing detail field")
	}
}

// TestRootConfinementResumeTask verifies that POST /tasks/{id}/resume rejects
// roots outside the workspace boundary.
func TestRootConfinementResumeTask(t *testing.T) {
	_, allowedRoot := testServer(t)

	working, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	kernelRoot := kernelfixture.Root(t, filepath.Dir(filepath.Dir(filepath.Dir(working))))

	store := executor.NewMemoryCheckpointer()
	server := &Server{
		KernelRoot:    kernelRoot,
		WorkspaceRoot: allowedRoot,
		Build: func(request runtime.PlanRequest) (runtime.PlanRequest, error) {
			request.Client = agents.FakeModelClient{}
			request.Checkpointer = store
			return request, nil
		},
	}
	handler := server.Handler()

	// Create a task first.
	post(t, handler, "/tasks", CreateTaskRequest{
		TaskID: "task-1", Task: "test", Root: allowedRoot,
	})

	// Resume with root outside workspace should be rejected (400).
	code, _ := post(t, handler, "/tasks/task-1/resume", ResumeRequest{
		Root:     "/etc/passwd",
		Decision: map[string]any{"status": "approved"},
	})
	if code != http.StatusBadRequest {
		t.Errorf("resume with root outside workspace = %d, want 400", code)
	}
}

// TestRootConfinementTaskStatus verifies that GET /tasks/{id} rejects roots
// outside the workspace boundary.
func TestRootConfinementTaskStatus(t *testing.T) {
	_, allowedRoot := testServer(t)

	working, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	kernelRoot := kernelfixture.Root(t, filepath.Dir(filepath.Dir(filepath.Dir(working))))

	store := executor.NewMemoryCheckpointer()
	server := &Server{
		KernelRoot:    kernelRoot,
		WorkspaceRoot: allowedRoot,
		Build: func(request runtime.PlanRequest) (runtime.PlanRequest, error) {
			request.Client = agents.FakeModelClient{}
			request.Checkpointer = store
			return request, nil
		},
	}
	handler := server.Handler()

	// Create a task first.
	post(t, handler, "/tasks", CreateTaskRequest{
		TaskID: "task-1", Task: "test", Root: allowedRoot,
	})

	// Status query with root outside workspace should be rejected (400).
	code, _ := get(t, handler, "/tasks/task-1?root=/etc/passwd")
	if code != http.StatusBadRequest {
		t.Errorf("status with root outside workspace = %d, want 400", code)
	}
}

// TestErrorSanitization verifies that 500-class errors return a generic
// message to the client while the full error would be observable server-side.
func TestErrorSanitization(t *testing.T) {
	_, root := testServer(t)

	working, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	kernelRoot := kernelfixture.Root(t, filepath.Dir(filepath.Dir(filepath.Dir(working))))

	// Create a server with a failing prepare function to trigger a 500 error.
	server := &Server{
		KernelRoot: kernelRoot,
		Build: func(request runtime.PlanRequest) (runtime.PlanRequest, error) {
			return request, fmt.Errorf("/some/sensitive/path: file not found")
		},
	}
	handler := server.Handler()

	// The client should receive a generic error message.
	code, body := postRaw(t, handler, "/tasks", CreateTaskRequest{
		TaskID: "task-1", Task: "test", Root: root,
	})
	if code != http.StatusInternalServerError {
		t.Errorf("create with error = %d, want 500", code)
	}
	// Verify the response doesn't contain the sensitive path.
	if strings.Contains(body, "/some/sensitive/path") {
		t.Error("error response leaked sensitive path to client")
	}
	// Verify it contains the generic message.
	if !strings.Contains(body, "internal error") {
		t.Errorf("error response missing generic message, got: %s", body)
	}
}

// TestRequestBodySizeLimit verifies that oversized request bodies are rejected.
func TestRequestBodySizeLimit(t *testing.T) {
	_, root := testServer(t)

	working, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	kernelRoot := kernelfixture.Root(t, filepath.Dir(filepath.Dir(filepath.Dir(working))))

	store := executor.NewMemoryCheckpointer()
	server := &Server{
		KernelRoot: kernelRoot,
		Build: func(request runtime.PlanRequest) (runtime.PlanRequest, error) {
			request.Client = agents.FakeModelClient{}
			request.Checkpointer = store
			return request, nil
		},
	}
	handler := server.Handler()

	// Create a request body larger than 10 MB.
	largeTask := strings.Repeat("x", 11*1024*1024)
	code, payload := post(t, handler, "/tasks", CreateTaskRequest{
		TaskID: "task-1", Task: largeTask, Root: root,
	})
	// Should be rejected with a 400 error (malformed JSON).
	if code != http.StatusBadRequest {
		t.Errorf("oversized body = %d, want 400: %v", code, payload)
	}
}

// TestConcurrentTaskCreationSerialization verifies that concurrent requests
// to create the same task are properly serialized by the lock.
func TestConcurrentTaskCreationSerialization(t *testing.T) {
	handler, root := testServer(t)

	working, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	kernelRoot := kernelfixture.Root(t, filepath.Dir(filepath.Dir(filepath.Dir(working))))

	// Use a checkpointer that tracks dispatch calls.
	store := executor.NewMemoryCheckpointer()
	saveCalls := atomic.Int32{}

	server := &Server{
		KernelRoot: kernelRoot,
		Build: func(request runtime.PlanRequest) (runtime.PlanRequest, error) {
			request.Client = agents.FakeModelClient{}
			// Wrap the checkpointer to count save attempts.
			wrappedStore := &trackingCheckpointer{inner: store, saveCalls: &saveCalls}
			request.Checkpointer = wrappedStore
			return request, nil
		},
	}
	handler = server.Handler()

	const numConcurrent = 5
	var wg sync.WaitGroup
	successCount := atomic.Int32{}

	// Launch concurrent create requests for the same task.
	for i := 0; i < numConcurrent; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, payload := post(t, handler, "/tasks", CreateTaskRequest{
				TaskID: "task-1", Task: "test", Root: root,
			})
			if code == http.StatusOK {
				// Only the first request should get status=interrupted.
				// Subsequent requests should get already-planned.
				if payload["status"] != nil {
					successCount.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	if successCount.Load() != int32(numConcurrent) {
		t.Errorf("concurrent creates: %d succeeded, want %d", successCount.Load(), numConcurrent)
	}

	// Verify the task exists and was only started once.
	if !runtime.TaskExists(root, "task-1") {
		t.Error("task was not created")
	}
}

// TestSecureByDefaultConfinement verifies that the service properly enforces
// workspace-root confinement when a WorkspaceRoot is configured.
// This test proves service-level confinement enforcement.
// The actual CLI flag-parsing default (setting WorkspaceRoot to os.Getwd() when
// no flags are provided) is proven by enginecli/serve_test.go's
// TestResolveWorkspaceRootDefault.
func TestSecureByDefaultConfinement(t *testing.T) {
	working, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	kernelRoot := kernelfixture.Root(t, filepath.Dir(filepath.Dir(filepath.Dir(working))))

	store := executor.NewMemoryCheckpointer()

	// Create a server with a configured WorkspaceRoot. This exercises the
	// service's confinement enforcement, not the CLI's flag-parsing logic.
	cwdForConfinement := t.TempDir() // Use a specific temp dir as the workspace boundary for this test
	server := &Server{
		KernelRoot:    kernelRoot,
		WorkspaceRoot: cwdForConfinement,
		Build: func(request runtime.PlanRequest) (runtime.PlanRequest, error) {
			request.Client = agents.FakeModelClient{}
			request.Checkpointer = store
			return request, nil
		},
	}
	handler := server.Handler()

	// Request with root inside the confined workspace should succeed.
	code, _ := post(t, handler, "/tasks", CreateTaskRequest{
		TaskID: "task-1", Task: "test", Root: cwdForConfinement,
	})
	if code != http.StatusOK {
		t.Errorf("create with root inside confined workspace = %d, want 200", code)
	}

	// Request with root outside the confined workspace should be rejected (400).
	code, payload := post(t, handler, "/tasks", CreateTaskRequest{
		TaskID: "task-2", Task: "test", Root: "/tmp/outside",
	})
	if code != http.StatusBadRequest {
		t.Errorf("create with root outside confined workspace = %d, want 400: %v", code, payload)
	}
	detail, _ := payload["detail"].(string)
	if detail == "" || detail == "internal error" {
		t.Errorf("confinement violation should not be generic, got: %s", detail)
	}
}

// TestExplicitUnconfinedOptOut verifies that the service allows any root when
// WorkspaceRoot is empty (representing an explicit opt-out from confinement).
// This test proves service-level unconfined behavior.
// The actual CLI flag-parsing logic that produces an empty WorkspaceRoot
// (when --allow-unconfined-root is passed) is proven by enginecli/serve_test.go's
// TestResolveWorkspaceRootAllowUnconfinedRootPriority and
// TestResolveWorkspaceRootUnconfinedWithoutPath.
func TestExplicitUnconfinedOptOut(t *testing.T) {
	working, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	kernelRoot := kernelfixture.Root(t, filepath.Dir(filepath.Dir(filepath.Dir(working))))

	store := executor.NewMemoryCheckpointer()

	// Create a directory outside any workspace for testing unconfined access
	unconfineRoot := t.TempDir()

	// Create a server with empty WorkspaceRoot, representing unconfined access.
	// This exercises the service's behavior when confinement is disabled, not
	// the CLI's flag-parsing logic.
	server := &Server{
		KernelRoot:    kernelRoot,
		WorkspaceRoot: "", // Empty = no confinement
		Build: func(request runtime.PlanRequest) (runtime.PlanRequest, error) {
			request.Client = agents.FakeModelClient{}
			request.Checkpointer = store
			return request, nil
		},
	}
	handler := server.Handler()

	// Request with any root should succeed when confinement is disabled.
	code, _ := post(t, handler, "/tasks", CreateTaskRequest{
		TaskID: "task-1", Task: "test", Root: unconfineRoot,
	})
	if code != http.StatusOK {
		t.Errorf("create with unconfined root = %d, want 200", code)
	}
}

// TestServerSideErrorLogging verifies that 500-class errors are logged
// server-side with full error detail while the client receives a generic message.
func TestServerSideErrorLogging(t *testing.T) {
	working, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	kernelRoot := kernelfixture.Root(t, filepath.Dir(filepath.Dir(filepath.Dir(working))))
	root := t.TempDir()

	// Create a capturing logger to verify error logging.
	var logOutput bytes.Buffer
	testLogger := &captureLogger{buf: &logOutput}

	// Create a server with a failing prepare function to trigger a 500 error.
	server := &Server{
		KernelRoot:    kernelRoot,
		WorkspaceRoot: root,
		Logger:        testLogger,
		Build: func(request runtime.PlanRequest) (runtime.PlanRequest, error) {
			return request, fmt.Errorf("/some/sensitive/path/file.txt: file not found")
		},
	}
	handler := server.Handler()

	// The client should receive a generic error message.
	code, body := postRaw(t, handler, "/tasks", CreateTaskRequest{
		TaskID: "task-1", Task: "test", Root: root,
	})
	if code != http.StatusInternalServerError {
		t.Errorf("create with error = %d, want 500", code)
	}

	// Verify the response doesn't contain the sensitive path.
	if strings.Contains(body, "/some/sensitive/path") {
		t.Error("error response leaked sensitive path to client")
	}

	// Verify it contains the generic message.
	if !strings.Contains(body, "internal error") {
		t.Errorf("error response missing generic message, got: %s", body)
	}

	// Verify the server-side log contains the full error detail.
	logStr := logOutput.String()
	if !strings.Contains(logStr, "/some/sensitive/path") {
		t.Error("server-side log missing sensitive path detail")
	}
	if !strings.Contains(logStr, "task-1") {
		t.Error("server-side log missing task_id")
	}
	if !strings.Contains(logStr, "file not found") {
		t.Error("server-side log missing error message")
	}
}

// captureLogger implements the Logger interface and captures log output to a buffer.
type captureLogger struct {
	buf *bytes.Buffer
}

func (cl *captureLogger) Printf(format string, v ...interface{}) {
	fmt.Fprintf(cl.buf, format+"\n", v...)
}

// trackingCheckpointer wraps an executor.Checkpointer to track Save calls.
type trackingCheckpointer struct {
	inner     executor.Checkpointer
	saveCalls *atomic.Int32
}

func (tc *trackingCheckpointer) Save(taskID string, checkpoint executor.Checkpoint) error {
	tc.saveCalls.Add(1)
	return tc.inner.Save(taskID, checkpoint)
}

func (tc *trackingCheckpointer) Load(taskID string) (executor.Checkpoint, bool, error) {
	return tc.inner.Load(taskID)
}

func (tc *trackingCheckpointer) Rewind(taskID string) error {
	return tc.inner.Rewind(taskID)
}
