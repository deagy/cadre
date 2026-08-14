package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// MCPDispatchServerCmd is the `cadre mcp-dispatch-server` command: runs the Codex MCP
// dispatch server on stdio, exposing dispatch_secure_cloud_role to a running Codex CLI
// session. Requires the optional `mcp` package (see requirements-mcp.txt).
//
// This is currently a thin wrapper that shells out to the Python implementation
// (roster/orchestration/mcp/dispatch_server.py). Full Go port of dispatch_core.py
// (3,107 lines of complex sandbox/runner/dispatch logic) is pending.
func MCPDispatchServerCmd(args []string, stdout, stderr io.Writer) int {
	// Parse --help early to avoid requiring Python/mcp package
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintf(stdout, "usage: cadre mcp-dispatch-server\n\n")
		fmt.Fprintf(stdout, "Run the Codex MCP dispatch server on stdio, exposing dispatch_secure_cloud_role\n")
		fmt.Fprintf(stdout, "to a running Codex CLI session. Takes no options: the transport is stdio only,\n")
		fmt.Fprintf(stdout, "and the server is started by an MCP client rather than run interactively.\n\n")
		fmt.Fprintf(stdout, "Requires the optional `mcp` package:\n")
		fmt.Fprintf(stdout, "  pip install -r roster/orchestration/mcp/requirements-mcp.txt\n")
		return 0
	}

	// Shell out to Python implementation
	repoRoot, err := findRepoRoot()
	if err != nil {
		fmt.Fprintf(stderr, "cadre mcp-dispatch-server: %v\n", err)
		return 1
	}

	pythonScript := filepath.Join(repoRoot, "roster", "orchestration", "mcp", "dispatch_server.py")
	code, err := defaultPythonExecutable(context.Background(), pythonScript, args, []string{}, stdout, stderr, os.Stdin)
	if err != nil {
		fmt.Fprintf(stderr, "cadre mcp-dispatch-server: %v\n", err)
		return 1
	}
	return code
}
