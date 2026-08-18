// Package service exposes the lifecycle over HTTP.
//
// Ported from engine/agentic_sdlc_langgraph/service.py, which was FastAPI.
// net/http rather than a framework: three routes and a status-code mapping do
// not need one, and a repository shipping static binaries pays for every
// dependency twice -- once in the module graph and once in the binary.
package service

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/deagy/cadre/cli/internal/engine/gitlabissue"
	"github.com/deagy/cadre/cli/internal/engine/runtime"
)

// Server serves the lifecycle routes.
type Server struct {
	// KernelRoot locates the shipped contracts.
	KernelRoot string
	// Build lets a test substitute how a run is assembled. Nil uses the real
	// runtime, which is what a deployment wants.
	Build func(runtime.PlanRequest) (runtime.PlanRequest, error)
}

// CreateTaskRequest is the body of POST /tasks.
type CreateTaskRequest struct {
	TaskID                  string   `json:"task_id"`
	Task                    string   `json:"task"`
	Root                    string   `json:"root"`
	Profile                 string   `json:"profile"`
	IgnoredGateIDs          []string `json:"ignored_gate_ids"`
	ProviderManifest        string   `json:"provider_manifest"`
	IntentGitLabIssue       string   `json:"intent_gitlab_issue"`
	RequirementsGitLabIssue string   `json:"requirements_gitlab_issue"`
	Classification          string   `json:"classification"`
}

// ResumeRequest is the body of POST /tasks/{id}/resume.
type ResumeRequest struct {
	Root     string         `json:"root"`
	Decision map[string]any `json:"decision"`
}

// Handler builds the route table.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /tasks", s.createTask)
	mux.HandleFunc("POST /tasks/{task_id}/resume", s.resumeTask)
	mux.HandleFunc("GET /tasks/{task_id}", s.taskStatus)
	return mux
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]any{"detail": detail})
}

// statusFor maps an error to a response code.
//
// A configuration conflict is 409 rather than 500: the request was
// well-formed and the server is healthy; what is wrong is that the task was
// planned differently, which is the caller's to resolve.
func statusFor(err error) int {
	var configErr runtime.ConfigError
	if errors.As(err, &configErr) {
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

func (s *Server) plan(root, taskID string) runtime.PlanRequest {
	return runtime.PlanRequest{Root: root, KernelRoot: s.KernelRoot, TaskID: taskID}
}

func (s *Server) prepare(request runtime.PlanRequest) (runtime.PlanRequest, error) {
	if s.Build == nil {
		return request, nil
	}
	return s.Build(request)
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	var body CreateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "request body is not valid JSON")
		return
	}
	if strings.TrimSpace(body.TaskID) == "" || strings.TrimSpace(body.Root) == "" {
		writeError(w, http.StatusUnprocessableEntity, "task_id and root are required")
		return
	}

	// An issue reference is resolved before anything is planned. A bad
	// reference is the caller's to fix (422), and resolving it first means a
	// task is never planned with a source id that turned out not to exist.
	intentRecordID, err := gitlabissue.ResolveIssueReference(body.IntentGitLabIssue)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	requirementsBaselineID, err := gitlabissue.ResolveIssueReference(body.RequirementsGitLabIssue)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	request := s.plan(body.Root, body.TaskID)
	request.TaskText = body.Task
	request.ProfileID = body.Profile
	request.ProviderManifest = body.ProviderManifest
	request.IgnoredGateIDs = body.IgnoredGateIDs

	prepared, err := s.prepare(request)
	if err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}

	payload, err := runtime.CreateOrReconnectTask(runtime.TaskRequest{
		PlanRequest: prepared, Classification: body.Classification,
		IntentRecordID: intentRecordID, RequirementsBaselineID: requirementsBaselineID,
	})
	if err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) resumeTask(w http.ResponseWriter, r *http.Request) {
	var body ResumeRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "request body is not valid JSON")
		return
	}
	taskID := r.PathValue("task_id")
	if strings.TrimSpace(body.Root) == "" {
		writeError(w, http.StatusUnprocessableEntity, "root is required")
		return
	}
	if !runtime.TaskExists(body.Root, taskID) {
		writeError(w, http.StatusNotFound, "no such task: "+taskID)
		return
	}

	prepared, err := s.prepare(s.plan(body.Root, taskID))
	if err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}
	payload, err := runtime.ResumeTask(prepared, body.Decision)
	if err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) taskStatus(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("task_id")
	root := r.URL.Query().Get("root")
	if strings.TrimSpace(root) == "" {
		writeError(w, http.StatusUnprocessableEntity, "root is required")
		return
	}
	if !runtime.TaskExists(root, taskID) {
		writeError(w, http.StatusNotFound, "no such task: "+taskID)
		return
	}

	prepared, err := s.prepare(s.plan(root, taskID))
	if err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}
	payload, err := runtime.TaskStatus(prepared)
	if err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, payload)
}
