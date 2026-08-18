package a2a

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The wire format, pinned against the pydantic it replaces.
//
// testdata/pydantic_wire.json was produced by the Python models themselves
// (pydantic 2.13.4) for each of the call sites that serialize differently. It
// is committed so the contract outlives the Python: once those models are
// deleted there is nothing left to re-derive it from, and "what bytes did we
// used to send" stops being an answerable question.
//
// The differences are not incidental. The client sends a message with
// exclude_none, the server dumps a task result *without* it and wraps that in
// an envelope *with* it, and the stream dumps events without it. So nulls are
// dropped in some documents and present in others, and an `omitempty` applied
// uniformly would be wrong in one direction or the other.
func loadPinnedWire(t *testing.T) map[string]any {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join("testdata", "pydantic_wire.json"))
	if err != nil {
		t.Fatalf("reading the pinned wire format: %v", err)
	}
	var pinned map[string]any
	if err := json.Unmarshal(contents, &pinned); err != nil {
		t.Fatalf("parsing the pinned wire format: %v", err)
	}
	return pinned
}

// renders marshals a value and re-decodes it, so comparison is structural
// rather than sensitive to key order.
func renders(t *testing.T, value any) any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	return decoded
}

func assertMatchesPinned(t *testing.T, name string, got any) {
	t.Helper()
	pinned := loadPinnedWire(t)
	want, ok := pinned[name]
	if !ok {
		t.Fatalf("testdata has no pinned document named %q", name)
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	// Both sides go through the same encoder, so key order is normalised.
	var gotNormal, wantNormal any
	_ = json.Unmarshal(gotJSON, &gotNormal)
	_ = json.Unmarshal(wantJSON, &wantNormal)
	if string(mustCanonical(t, gotNormal)) != string(mustCanonical(t, wantNormal)) {
		t.Errorf("%s does not match the pydantic wire format\n  go:     %s\n  python: %s",
			name, mustCanonical(t, gotNormal), mustCanonical(t, wantNormal))
	}
}

func mustCanonical(t *testing.T, value any) []byte {
	t.Helper()
	// encoding/json sorts map keys, which is what makes this canonical.
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("canonicalising: %v", err)
	}
	return encoded
}

func TestClientMessageMatchesPydantic(t *testing.T) {
	minimal, err := ExcludeNone(NewMessage(NewTextPart("hi")))
	if err != nil {
		t.Fatalf("ExcludeNone: %v", err)
	}
	assertMatchesPinned(t, "client_message_min", minimal)

	taskID, contextID := "t1", "c1"
	full, err := ExcludeNone(Message{
		Role:      "agent",
		Parts:     []TextPart{NewTextPart("hi")},
		TaskID:    &taskID,
		ContextID: &contextID,
		Metadata:  map[string]any{"k": 1},
	})
	if err != nil {
		t.Fatalf("ExcludeNone: %v", err)
	}
	assertMatchesPinned(t, "client_message_full", full)
}

func TestTaskResultMatchesPydantic(t *testing.T) {
	task := Task{ID: "t1", ContextID: "c1", Status: TaskStatus{State: TaskWorking}}
	assertMatchesPinned(t, "task_result", renders(t, task))
}

func TestRPCEnvelopesMatchPydantic(t *testing.T) {
	task := Task{ID: "t1", ContextID: "c1", Status: TaskStatus{State: TaskWorking}}

	// The inner result is dumped verbatim; only the envelope is pruned.
	response := NewJSONRPCResponse(1)
	response.Result = task
	ok, err := response.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	assertMatchesPinned(t, "rpc_ok", ok)

	failure := NewJSONRPCResponse(1)
	failure.Error = map[string]any{"code": -32600, "message": "bad"}
	errored, err := failure.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	assertMatchesPinned(t, "rpc_err", errored)
}

func TestStreamEventMatchesPydantic(t *testing.T) {
	event := TaskStatusUpdateEvent{
		TaskID: "t1", ContextID: "c1",
		Status: TaskStatus{State: TaskCompleted}, Final: true,
	}
	assertMatchesPinned(t, "sse_event", renders(t, event))
}

// A nil slice must never reach the wire as null.
//
// This is the Go-specific hazard in this package: pydantic defaults these
// fields to empty lists, so they are always arrays. Go marshals a nil slice as
// null, and a zero-value Task or Message is the easiest thing in the world to
// construct.
func TestDefaultedListsRenderAsArraysNotNull(t *testing.T) {
	encoded, err := json.Marshal(Task{ID: "t", ContextID: "c"})
	if err != nil {
		t.Fatalf("marshalling a zero Task: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	for _, field := range []string{"history", "artifacts"} {
		value, present := document[field]
		if !present {
			t.Errorf("a zero Task omits %q entirely", field)
			continue
		}
		if _, isList := value.([]any); !isList {
			t.Errorf("a zero Task renders %q as %v, want []", field, value)
		}
	}

	encoded, err = json.Marshal(Message{Role: "user"})
	if err != nil {
		t.Fatalf("marshalling a zero Message: %v", err)
	}
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if _, isList := document["parts"].([]any); !isList {
		t.Errorf("a zero Message renders parts as %v, want []", document["parts"])
	}
}

// exclude_none drops null *keys*, not null list elements, and leaves empty
// containers alone.
func TestExcludeNoneDropsKeysNotContainers(t *testing.T) {
	pruned, err := ExcludeNone(map[string]any{
		"gone":   nil,
		"kept":   "value",
		"empty":  []any{},
		"nested": map[string]any{"gone": nil, "kept": 1},
		"list":   []any{nil, "x"},
	})
	if err != nil {
		t.Fatalf("ExcludeNone: %v", err)
	}
	document := pruned.(map[string]any)
	if _, present := document["gone"]; present {
		t.Error("a null key survived")
	}
	if _, present := document["empty"]; !present {
		t.Error("an empty list was dropped; exclude_none drops nulls, not empties")
	}
	if nested := document["nested"].(map[string]any); len(nested) != 1 {
		t.Errorf("nested pruning produced %v, want only the non-null key", nested)
	}
	if list := document["list"].([]any); len(list) != 2 || list[0] != nil {
		t.Errorf("list = %v, want the null element preserved", list)
	}
}
