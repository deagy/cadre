package orchestration

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// PythonCLIBridge provides integration with Python CLI tools.
type PythonCLIBridge struct {
	pythonPath string
	scriptDir  string
}

// NewPythonCLIBridge creates a new Python CLI bridge.
func NewPythonCLIBridge(pythonPath, scriptDir string) *PythonCLIBridge {
	if pythonPath == "" {
		pythonPath = "python3"
	}
	return &PythonCLIBridge{
		pythonPath: pythonPath,
		scriptDir:  scriptDir,
	}
}

// PythonRequest represents a request to Python CLI.
type PythonRequest struct {
	Script string      `json:"script"`
	Action string      `json:"action"`
	Args   interface{} `json:"args"`
	Input  interface{} `json:"input"`
}

// PythonResponse represents a response from Python CLI.
type PythonResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data"`
	Error   string      `json:"error"`
	Status  string      `json:"status"`
}

// InvokePython invokes a Python script via CLI.
func (pcb *PythonCLIBridge) InvokePython(script, action string, args interface{}) (*PythonResponse, error) {
	scriptPath := filepath.Join(pcb.scriptDir, script)

	// Check if script exists
	if _, err := os.Stat(scriptPath); err != nil {
		return &PythonResponse{
			Success: false,
			Error:   fmt.Sprintf("script not found: %s", scriptPath),
		}, err
	}

	// Marshal args to JSON
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal args: %w", err)
	}

	// Execute Python script
	cmd := exec.Command(pcb.pythonPath, scriptPath, "--action", action, "--input", string(argsJSON))
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")

	output, err := cmd.Output()
	if err != nil {
		return &PythonResponse{
			Success: false,
			Error:   fmt.Sprintf("execution failed: %v", err),
		}, err
	}

	// Parse response
	var response PythonResponse
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &response, nil
}

// RouteSelector uses Python CLI for route selection.
func (pcb *PythonCLIBridge) RouteSelector(task, classification string, files []string) ([]string, error) {
	args := map[string]interface{}{
		"task":           task,
		"classification": classification,
		"changed_files":  files,
	}

	response, err := pcb.InvokePython("route_selector.py", "select_routes", args)
	if err != nil {
		return nil, err
	}

	if !response.Success {
		return nil, fmt.Errorf("route selection failed: %s", response.Error)
	}

	// Extract route IDs from response
	routes, ok := response.Data.([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected response type: %T", response.Data)
	}

	var routeIDs []string
	for _, route := range routes {
		if routeID, ok := route.(string); ok {
			routeIDs = append(routeIDs, routeID)
		}
	}

	return routeIDs, nil
}

// KnowledgeStoreClient uses Python CLI for knowledge store access.
type KnowledgeStoreClient struct {
	bridge *PythonCLIBridge
}

// NewKnowledgeStoreClient creates a new knowledge store client.
func NewKnowledgeStoreClient(bridge *PythonCLIBridge) *KnowledgeStoreClient {
	return &KnowledgeStoreClient{bridge: bridge}
}

// QueryKnowledge queries the knowledge store.
func (ksc *KnowledgeStoreClient) QueryKnowledge(query string, source string) (interface{}, error) {
	args := map[string]interface{}{
		"query":  query,
		"source": source,
	}

	response, err := ksc.bridge.InvokePython("knowledge_store.py", "query", args)
	if err != nil {
		return nil, err
	}

	if !response.Success {
		return nil, fmt.Errorf("knowledge query failed: %s", response.Error)
	}

	return response.Data, nil
}

// StoreKnowledge stores knowledge in the knowledge store.
func (ksc *KnowledgeStoreClient) StoreKnowledge(title, content, source string, metadata map[string]interface{}) error {
	args := map[string]interface{}{
		"title":    title,
		"content":  content,
		"source":   source,
		"metadata": metadata,
	}

	response, err := ksc.bridge.InvokePython("knowledge_store.py", "store", args)
	if err != nil {
		return err
	}

	if !response.Success {
		return fmt.Errorf("knowledge store failed: %s", response.Error)
	}

	return nil
}

// PythonCompatibilityLayer provides backward compatibility with Python CLI.
type PythonCompatibilityLayer struct {
	bridge *PythonCLIBridge
}

// NewPythonCompatibilityLayer creates a new compatibility layer.
func NewPythonCompatibilityLayer(bridge *PythonCLIBridge) *PythonCompatibilityLayer {
	return &PythonCompatibilityLayer{bridge: bridge}
}

// MigrateConfig migrates Python CLI configuration to Go format.
func (pcl *PythonCompatibilityLayer) MigrateConfig(pythonConfigPath string) (map[string]interface{}, error) {
	args := map[string]interface{}{
		"config_path": pythonConfigPath,
	}

	response, err := pcl.bridge.InvokePython("config_migrator.py", "migrate", args)
	if err != nil {
		return nil, err
	}

	if !response.Success {
		return nil, fmt.Errorf("config migration failed: %s", response.Error)
	}

	config, ok := response.Data.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected response type: %T", response.Data)
	}

	return config, nil
}
