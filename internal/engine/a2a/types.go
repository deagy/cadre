// Package a2a models the narrow subset of the public A2A (Agent2Agent) wire
// format this engine speaks: agent-card discovery, a JSON-RPC 2.0 envelope,
// and the Task/Message shapes exchanged by message/send, tasks/get and
// message/stream.
//
// Deliberately narrow, matching the Python it is ported from: text parts only
// (no file or data parts), and no push-notification or auth-scheme types.
//
// Ported from engine/agentic_sdlc_langgraph/a2a/types.py.
package a2a

import "encoding/json"

// TaskState is the lifecycle state of an A2A task.
type TaskState string

const (
	TaskSubmitted     TaskState = "submitted"
	TaskWorking       TaskState = "working"
	TaskInputRequired TaskState = "input-required"
	TaskCompleted     TaskState = "completed"
	TaskFailed        TaskState = "failed"
	TaskCanceled      TaskState = "canceled"
)

// TextPart is the only part kind modeled.
type TextPart struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// NewTextPart builds a part with the constant discriminator set.
//
// `kind` is a Literal["text"] with a default in pydantic, so it is always on
// the wire even when a caller never sets it. A bare TextPart{} in Go would
// marshal `"kind": ""`, which no A2A peer accepts.
func NewTextPart(text string) TextPart {
	return TextPart{Kind: "text", Text: text}
}

// Message is one turn exchanged with an agent.
//
// The optional fields are pointers so that "absent" and "present but empty"
// stay distinguishable: the client drops nils before sending, and the server
// renders them as null inside a task result. Collapsing the two would change
// the bytes on the wire in one of those two directions.
type Message struct {
	Role      string         `json:"role"`
	Parts     []TextPart     `json:"parts"`
	TaskID    *string        `json:"taskId"`
	ContextID *string        `json:"contextId"`
	Metadata  map[string]any `json:"metadata"`
}

// NewMessage builds a user message with pydantic's defaults applied.
func NewMessage(parts ...TextPart) Message {
	if parts == nil {
		parts = []TextPart{}
	}
	return Message{Role: "user", Parts: parts}
}

// MarshalJSON renders `parts` as [] rather than null when unset.
//
// pydantic defaults it to an empty list, so the field is always an array on
// the wire. Go marshals a nil slice as null, which is a different document and
// one a strict peer may reject.
func (m Message) MarshalJSON() ([]byte, error) {
	type wire Message
	copied := wire(m)
	if copied.Parts == nil {
		copied.Parts = []TextPart{}
	}
	return json.Marshal(copied)
}

// TaskStatus is a task's state plus optional detail.
//
// `message` and `timestamp` are rendered as null rather than omitted: the
// server dumps a task result without exclude_none, so both keys appear.
type TaskStatus struct {
	State     TaskState `json:"state"`
	Message   any       `json:"message"`
	Timestamp *string   `json:"timestamp"`
}

// Task is an A2A task as returned by message/send and tasks/get.
type Task struct {
	ID        string     `json:"id"`
	ContextID string     `json:"contextId"`
	Status    TaskStatus `json:"status"`
	History   []Message  `json:"history"`
	Artifacts []any      `json:"artifacts"`
}

// MarshalJSON renders `history` and `artifacts` as [] rather than null.
func (t Task) MarshalJSON() ([]byte, error) {
	type wire Task
	copied := wire(t)
	if copied.History == nil {
		copied.History = []Message{}
	}
	if copied.Artifacts == nil {
		copied.Artifacts = []any{}
	}
	return json.Marshal(copied)
}

// TaskStatusUpdateEvent is one server-sent event on message/stream.
//
// Streamed with no null-pruning, so its nested status keys appear even when
// empty.
type TaskStatusUpdateEvent struct {
	TaskID    string     `json:"taskId"`
	ContextID string     `json:"contextId"`
	Status    TaskStatus `json:"status"`
	Final     bool       `json:"final"`
}

// AgentCard is the discovery document served at the well-known path.
type AgentCard struct {
	Name               string           `json:"name"`
	Description        string           `json:"description"`
	URL                string           `json:"url"`
	Version            string           `json:"version"`
	Capabilities       map[string]bool  `json:"capabilities"`
	DefaultInputModes  []string         `json:"defaultInputModes"`
	DefaultOutputModes []string         `json:"defaultOutputModes"`
	Skills             []map[string]any `json:"skills"`
}

// JSONRPCRequest is the request envelope.
type JSONRPCRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
}

// JSONRPCResponse is the response envelope.
type JSONRPCResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Result  any    `json:"result"`
	Error   any    `json:"error"`
}

// NewJSONRPCResponse builds a response envelope with the version set.
func NewJSONRPCResponse(id any) JSONRPCResponse {
	return JSONRPCResponse{JSONRPC: "2.0", ID: id}
}

// ExcludeNone renders a value the way pydantic's model_dump(exclude_none=True)
// does: every key whose value is null is dropped, recursively.
//
// A function rather than struct tags, because in the Python this is a
// *call-site* decision and the two sites disagree. The client sends a message
// with exclude_none, while the server dumps a task result without it and wraps
// that in an envelope with it -- so the same Message type appears on the wire
// both pruned and unpruned. `omitempty` on the struct would bake one of those
// choices in and silently change the other.
//
// Empty containers survive: exclude_none drops nulls, not empty lists, so
// "history": [] stays.
func ExcludeNone(value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, err
	}
	return pruneNulls(decoded), nil
}

func pruneNulls(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		pruned := make(map[string]any, len(typed))
		for key, nested := range typed {
			if nested == nil {
				continue
			}
			pruned[key] = pruneNulls(nested)
		}
		return pruned
	case []any:
		pruned := make([]any, 0, len(typed))
		for _, nested := range typed {
			// A null *element* is a value in a list, not an absent key;
			// pydantic keeps it.
			pruned = append(pruned, pruneNulls(nested))
		}
		return pruned
	default:
		return value
	}
}

// Render renders a response envelope exactly as the server does:
//
//	JSONRPCResponse(id=rpc.id, result=result.model_dump()).model_dump(exclude_none=True)
//
// The order matters and is not decorative. The result is rendered *first*, as
// plain data with its nulls intact, and only then is the envelope pruned.
// pydantic's exclude_none walks its own model fields; a value typed `Any` that
// already holds a dict is opaque to it, so nulls inside a task result survive
// while a null `error` key on the envelope does not.
//
// Pruning the whole tree instead -- the obvious Go implementation -- silently
// drops `status.message` and `status.timestamp` from every successful
// response. testdata/pydantic_wire.json is what caught that.
func (r JSONRPCResponse) Render() (map[string]any, error) {
	rendered := map[string]any{"jsonrpc": r.JSONRPC, "id": r.ID}

	if r.Result != nil {
		result, err := toPlainData(r.Result)
		if err != nil {
			return nil, err
		}
		rendered["result"] = result
	}
	if r.Error != nil {
		failure, err := toPlainData(r.Error)
		if err != nil {
			return nil, err
		}
		rendered["error"] = failure
	}

	// Only the envelope's own keys are subject to pruning, and `id` may
	// legitimately be null in JSON-RPC -- pydantic drops it under
	// exclude_none, so this does too.
	for key, value := range rendered {
		if value == nil {
			delete(rendered, key)
		}
	}
	return rendered, nil
}

// toPlainData renders a value to maps and slices without pruning anything.
func toPlainData(value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}
