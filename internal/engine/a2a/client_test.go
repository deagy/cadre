package a2a

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A fake agent: serves a card pointing at itself, and answers JSON-RPC.
func fakeAgent(t *testing.T, cardURL func(serverURL string) string, result any, rpcError any) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var server *httptest.Server

	mux.HandleFunc("/.well-known/agent.json", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"url": cardURL(server.URL)})
	})
	mux.HandleFunc("/rpc", func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		response := map[string]any{"jsonrpc": "2.0", "id": request["id"]}
		if rpcError != nil {
			response["error"] = rpcError
		} else {
			response["result"] = result
		}
		_ = json.NewEncoder(w).Encode(response)
	})

	server = httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func aTask() map[string]any {
	return map[string]any{
		"id": "task-1", "contextId": "ctx-1",
		"status":  map[string]any{"state": "completed"},
		"history": []any{}, "artifacts": []any{},
	}
}

// Plain http is refused unless the host is a recognised local-dev host.
//
// Not cosmetic: this endpoint carries role prompts and task content, and a
// misconfigured base_url would send all of it in cleartext.
func TestRequireHTTPSOrLocal(t *testing.T) {
	allowed := []string{
		"https://agent.example.com",
		"http://localhost:8080",
		"http://127.0.0.1:9000",
		"http://[::1]:9000",
	}
	for _, endpoint := range allowed {
		if err := RequireHTTPSOrLocal(endpoint, "A2A endpoint"); err != nil {
			t.Errorf("RequireHTTPSOrLocal(%q) = %v, want allowed", endpoint, err)
		}
	}

	refused := []string{
		"http://agent.example.com",
		"http://10.0.0.5:8080",
		"ftp://agent.example.com",
		"http://localhost.evil.com",
	}
	for _, endpoint := range refused {
		if err := RequireHTTPSOrLocal(endpoint, "A2A endpoint"); err == nil {
			t.Errorf("RequireHTTPSOrLocal(%q) = nil, want refusal", endpoint)
		}
	}
}

func TestSendMessageAndGetTask(t *testing.T) {
	server := fakeAgent(t, func(url string) string { return url + "/rpc" }, aTask(), nil)

	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	task, err := client.SendMessage("do the thing", "", nil)
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if task.ID != "task-1" || task.Status.State != TaskCompleted {
		t.Errorf("decoded %+v", task)
	}

	if _, err := client.GetTask("task-1"); err != nil {
		t.Errorf("GetTask: %v", err)
	}
}

// The agent card's url is data. Without an origin check it could redirect
// every subsequent RPC -- carrying role prompts and task content -- elsewhere.
func TestTheAgentCardCannotRedirectTheRPCOrigin(t *testing.T) {
	server := fakeAgent(t, func(string) string { return "https://somewhere-else.example.com/rpc" }, aTask(), nil)

	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, err = client.SendMessage("do the thing", "", nil)
	if err == nil {
		t.Fatal("a card pointing at another origin was accepted")
	}
	if !strings.Contains(err.Error(), "origin does not match") {
		t.Errorf("error was %q, want it to name the origin mismatch", err)
	}
}

// task_id travels in metadata, never as Message.taskId: the server reserves
// that field for continuing an existing task, so setting it would resume
// rather than create.
func TestSendMessagePutsTaskIDInMetadataNotTaskId(t *testing.T) {
	var captured map[string]any
	mux := http.NewServeMux()
	var server *httptest.Server
	mux.HandleFunc("/.well-known/agent.json", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"url": server.URL + "/rpc"})
	})
	mux.HandleFunc("/rpc", func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		params, _ := request["params"].(map[string]any)
		captured, _ = params["message"].(map[string]any)
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": aTask()})
	})
	server = httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := client.SendMessage("hello", "task-42", nil); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	if _, present := captured["taskId"]; present {
		t.Error("taskId was set on the message; that field means 'continue', not 'create'")
	}
	metadata, _ := captured["metadata"].(map[string]any)
	if metadata["task_id"] != "task-42" {
		t.Errorf("metadata.task_id = %v, want task-42", metadata["task_id"])
	}
}

// ContinueTask is the other path, and does set taskId.
func TestContinueTaskSetsTaskID(t *testing.T) {
	var captured map[string]any
	mux := http.NewServeMux()
	var server *httptest.Server
	mux.HandleFunc("/.well-known/agent.json", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"url": server.URL + "/rpc"})
	})
	mux.HandleFunc("/rpc", func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		params, _ := request["params"].(map[string]any)
		captured, _ = params["message"].(map[string]any)
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": aTask()})
	})
	server = httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client, _ := NewClient(server.URL, server.Client())
	if _, err := client.ContinueTask("task-42", map[string]any{"approved": true}); err != nil {
		t.Fatalf("ContinueTask: %v", err)
	}
	if captured["taskId"] != "task-42" {
		t.Errorf("taskId = %v, want task-42", captured["taskId"])
	}
}

func TestAnRPCErrorIsReported(t *testing.T) {
	server := fakeAgent(t, func(url string) string { return url + "/rpc" }, nil,
		map[string]any{"code": -32600, "message": "bad request"})

	client, _ := NewClient(server.URL, server.Client())
	if _, err := client.SendMessage("x", "", nil); err == nil {
		t.Fatal("an rpc error response was treated as success")
	} else if !strings.Contains(err.Error(), "A2A error") {
		t.Errorf("error was %q", err)
	}
}
