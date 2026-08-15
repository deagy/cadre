package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/deagy/cadre/cli/internal/config"
	"github.com/deagy/cadre/cli/internal/orchestration"
)

// MCPGitLabServerCmd runs the GitLab evidence MCP server on stdio.
//
// A port of roster/orchestration/mcp/gitlab_server.py. That script had no
// bin/cadre subcommand and was documented as "invoke it directly with
// python3"; it is a subcommand now, which is also how it stops depending on a
// `pip install mcp` step the rest of the CLI does not need.
func MCPGitLabServerCmd(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("cadre mcp-gitlab-server", flag.ContinueOnError)
	flags.SetOutput(stderr)
	auditPath := flags.String("audit-path", "",
		"Override the audit log path (default: ~/.agents/mcp-gitlab/audit.jsonl)")
	flags.Usage = func() {
		_, _ = fmt.Fprint(stdout, `usage: cadre mcp-gitlab-server [--audit-path PATH]

Run the GitLab evidence MCP server on stdio, exposing three create-only tools:

  create_review_subtask   Create a review-subtask issue under a parent issue
  write_wiki_page         Create or update a wiki page (human confirmation required)
  write_evidence_comment  Comment structured evidence on an existing issue

None of them can close, reopen, resolve, or relabel an issue.

Configuration (token, project, base URL) resolves exactly as it does for
`+"`cadre gitlab-evidence`"+`. See roster/orchestration/GITLAB-EVIDENCE.md
for the operator setup requirements, which are requirements, not guidance.
`)
	}
	if err := flags.Parse(args); err != nil {
		return 2
	}

	// Unconditional, before anything can resolve a setting. stdin is the
	// protocol channel here, so a prompt would both hang and corrupt the
	// stream by consuming a JSON-RPC frame as its answer.
	config.DisableInteractive()
	// This server is long-lived and project-agnostic: its cwd is wherever the
	// host launched it and has no relationship to whichever project a given
	// tool call concerns. Guessing a project from cwd would resolve one
	// project's GitLab settings for another project's evidence.
	config.DisableProjectTierCWDFallback()

	server := orchestration.NewGitLabMCPServer(*auditPath)
	if err := runMCPServer(context.Background(), "cadre-gitlab-evidence", server,
		os.Stdin, stdout, stderr); err != nil {
		_, _ = fmt.Fprintf(stderr, "cadre: mcp-gitlab-server error: %v\n", err)
		return 1
	}
	return 0
}
