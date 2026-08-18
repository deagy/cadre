package generators

// Code in this file is the substitution vocabulary of the Cline port.
//
// Extracted mechanically from plugin/tools/port_cline_agents.py's
// PATH_SUBSTITUTIONS, SKILL_PATH_SUBSTITUTIONS, ROLE_OVERRIDES and TOOL_MAP
// rather than retyped. Every entry is an exact substring match applied in
// order, and a single transcription slip would change one of 159 generated
// files in a way that reads as intentional. The reasoning behind individual
// entries lives with them in the Python original and in
// cline-plugins/cline-agents/README.md's "Path-reference rewrites" section.
//
// Order is load-bearing: longer, more specific phrases come first, because a
// shorter rule would otherwise consume their interior text.

type substitution struct{ from, to string }

var clinePathSubstitutions = []substitution{
	{"files under roster/shared/ are embedded verbatim", "these shared-policy defaults are embedded verbatim"},
	{"`../../review/halt-authority/AGENT.md`", "the halt-authority role definition"},
	{"`../../review/classification-and-marking-gate/AGENT.md`", "the classification-and-marking-gate role definition"},
	{"`../../shared/output-schemas/finding.schema.json`", "this project's finding output schema"},
	{"`../../shared/agent-autonomy.yaml`", "this project's agent-autonomy policy documentation"},
	{"`../../shared/team-profile.yaml`", "this project's team-profile documentation"},
	{"`../../shared/technology-standards.md`", "this project's technology-standards documentation"},
	{"`../../shared/library-standards.yaml`", "this project's library-standards documentation"},
	{"`../../shared/secure-development-policy.md`", "this project's secure-development-policy documentation"},
	{"`../../shared/operating-principles.md`", "this project's operating-principles documentation"},
	{"`../../shared/knowledge-use-policy.md`", "this project's knowledge-use-policy documentation"},
	{"`../../shared/context-use-policy.md`", "this project's context-use-policy documentation"},
	{"`roster/shared/context-use-policy.md`", "this project's context-use-policy documentation"},
	{"roster/shared/context-use-policy.md", "this project's context-use-policy documentation"},
	{"`roster/shared/knowledge-use-policy.md`", "this project's knowledge-use-policy documentation"},
	{"roster/shared/knowledge-use-policy.md", "this project's knowledge-use-policy documentation"},
	{"`roster/context-store/SECURITY.md`", "this project's context-store security documentation"},
	{"`roster/orchestration/handoff-contracts.md`", "this project's handoff-contracts documentation"},
	{"roster/orchestration/handoff-contracts.md", "this project's handoff-contracts documentation"},
	{"`../../shared/cloud-guardrails.md`", "this project's cloud-guardrails documentation"},
	{"`../../orchestration/escalation-policy.md`", "this project's escalation-policy documentation"},
	{"`../../orchestration/handoff-contracts.md`", "this project's handoff-contracts documentation"},
	{"`../shared/agent-autonomy.yaml`", "this project's agent-autonomy policy documentation"},
	{"`../shared/team-profile.yaml`", "this project's team-profile documentation"},
	{"`../shared/technology-standards.md`", "this project's technology-standards documentation"},
	{"`../shared/operating-principles.md`", "this project's operating-principles documentation"},
	{"`../shared/knowledge-use-policy.md`", "this project's knowledge-use-policy documentation"},
	{"`../documentation/evidence-curator/AGENT.md`", "this project's evidence-curator role definition"},
	{"`SECURITY.md`", "this project's security documentation"},
	{"`roster/knowledge-store/README.md`", "this project's knowledge-store documentation"},
	{"`../../knowledge-store/AGENT.md`", "this project's knowledge-store-steward role definition"},
	{"`roster/knowledge-store/proposed-knowledge.schema.json`", "this project's staged-knowledge-record schema"},
	{"`roster/knowledge-store/proposed-knowledge/`", "this project's staged-knowledge-record directory"},
	{"`proposed-knowledge.schema.json`", "this project's staged-knowledge-record schema"},
	{"`proposed-knowledge/`", "this project's staged-knowledge-record directory"},
	{"`roster/shared/`", "this project's shared-policy directory"},
	{"`roster/shared/README.md`", "this project's shared-policy documentation"},
	{"roster/shared/README.md", "this project's shared-policy documentation"},
	{"`roster/runner-capabilities.json`", "this project's runner-capability manifest"},
	{"`roster/knowledge-store/src/config.py`", "this project's knowledge-store configuration module"},
	{"`roster/knowledge-store/SECURITY.md`", "this project's knowledge-store security documentation"},
	{"`roster/RUNBOOK.md`", "this project's operating runbook"},
	{"roster/RUNBOOK.md", "this project's operating runbook"},
}

var clineSkillSubstitutions = []substitution{
	{"`../../suite/roster/`", "the bundled suite policy directory"},
	{"`roster/shared/workspace-isolation.md`", "this project's workspace-isolation policy documentation"},
	{"roster/shared/workspace-isolation.md", "this project's workspace-isolation policy documentation"},
	{"use the project-local suite when it\ncontains `roster/catalog.yaml`; otherwise use the self-contained suite under\n`../../suite/roster/` relative to this packaged skill", "this is entirely handled for you: this plugin's tools resolve the bundled role catalog on their own, with no config step needed before first dispatch"},
	{"`roster/catalog-order.txt`", "the bundled role catalog's ordering file"},
	{"roster/catalog-order.txt", "the bundled role catalog's ordering file"},
	{"the current `roster/catalog.yaml`", "the current bundled role catalog"},
	{"`roster/catalog.yaml`", "this repository's bundled role catalog"},
	{"roster/catalog.yaml", "this repository's bundled role catalog"},
	{"`roster/orchestration/routing.json`", "this repository's bundled routing configuration"},
	{"roster/orchestration/routing.json", "this repository's bundled routing configuration"},
	{"`roster/orchestration/src/select_agents.py`", "the bundled selector implementation"},
	{"roster/orchestration/src/select_agents.py", "the bundled selector implementation"},
	{"`roster/orchestration/src/build_dispatch_plan.py`", "the bundled dispatch-plan builder"},
	{"roster/orchestration/src/build_dispatch_plan.py", "the bundled dispatch-plan builder"},
	{"`roster/orchestration/escalation-policy.md`", "this repository's escalation-policy documentation"},
	{"`roster/orchestration/handoff-contracts.md`", "this repository's handoff-contracts documentation"},
	{"`roster/RUNBOOK.md`", "this repository's runbook"},
	{"roster/RUNBOOK.md", "this repository's runbook"},
	{"`roster/shared/`", "this project's shared-policy directory"},
	{"`roster/shared/README.md`", "this project's shared-policy documentation"},
	{"roster/shared/README.md", "this project's shared-policy documentation"},
	{"`roster/knowledge-store/README.md`", "this project's knowledge-store documentation"},
	{"`../../knowledge-store/AGENT.md`", "this project's knowledge-store-steward role definition"},
	{"`roster/knowledge-store/proposed-knowledge.schema.json`", "this project's staged-knowledge-record schema"},
	{"`roster/knowledge-store/proposed-knowledge/`", "this project's staged-knowledge-record directory"},
	{"`proposed-knowledge.schema.json`", "this project's staged-knowledge-record schema"},
	{"`proposed-knowledge/`", "this project's staged-knowledge-record directory"},
	{"`roster/knowledge-store/SECURITY.md`", "this project's knowledge-store security documentation"},
	{"roster/knowledge-store/SECURITY.md", "this project's knowledge-store security documentation"},
	{"`roster/orchestration/mcp/dispatch_core.py`", "the bundled MCP dispatch server implementation"},
	{"[`run-agent-orchestration`](../run-agent-orchestration/SKILL.md)", "the `run-agent-orchestration` skill"},
	{"`../run-agent-orchestration/SKILL.md`", "the `run-agent-orchestration` skill"},
	{"../run-agent-orchestration/SKILL.md", "the `run-agent-orchestration` skill"},
	{"`roster/engineering/backend-engineer/AGENT.md`", "its own role-definition file"},
	{"`roster/<domain>/<agent-name>/AGENT.md`", "its own role-definition file"},
	{"`roster/<phase>/<role>/AGENT.md`", "its own role-definition file"},
	{"`roster/knowledge-store/src/config.py`", "the bundled knowledge-store config-resolution logic"},
	{"roster/knowledge-store/src/config.py", "the bundled knowledge-store config-resolution logic"},
	{"Run the knowledge-store tests before ingestion: `python3 -m unittest discover -s roster/knowledge-store/test -p \"test_*.py\"`.", "Run the knowledge-store tests before ingestion if working from a checkout of the source register (this bundled plugin does not ship that test suite itself)."},
	{"`roster/knowledge-store/test`", "the bundled knowledge-store test suite"},
	{"roster/knowledge-store/test", "the bundled knowledge-store test suite"},
	{"`roster/orchestration/SECURITY-CONTROLS.md`", "the bundled security-controls register"},
	{"roster/orchestration/SECURITY-CONTROLS.md", "the bundled security-controls register"},
	{"1. `pip install -r roster/orchestration/mcp/requirements-mcp.txt` (installs\n       the official `mcp` SDK; stdio transport only \u2014 do not add a networked\n       extra).", "1. Install the official `mcp` SDK (stdio transport only \u2014 do not add a networked extra) if working from a checkout of the source register (this bundled plugin does not ship the MCP dispatch server's own dependency pin file)."},
	{"directly at `python3 <repo>/roster/orchestration/mcp/dispatch_server.py`", "directly at the bundled MCP dispatch server implementation, if working from a checkout of the source register (this bundled plugin does not ship that server as a standalone script)"},
	{"`roster/orchestration/mcp/dispatch_server.py`", "the bundled MCP dispatch server implementation"},
	{"roster/orchestration/mcp/dispatch_server.py", "the bundled MCP dispatch server implementation"},
	{"`roster/orchestration/mcp/requirements-mcp.txt`", "the bundled MCP dispatch server's dependency pin file"},
	{"roster/orchestration/mcp/requirements-mcp.txt", "the bundled MCP dispatch server's dependency pin file"},
	{"`roster/orchestration/runs/<task-id>/`", "this repository's local run-artifact directory, under a `<task-id>/` subdirectory,"},
	{"`roster/orchestration/runs/`", "this repository's local run-artifact directory"},
	{"roster/orchestration/runs/", "this repository's local run-artifact directory"},
	{"`roster/orchestration/src/generate_role_metadata.py`", "the bundled role-metadata generator"},
	{"roster/orchestration/src/generate_role_metadata.py", "the bundled role-metadata generator"},
	{"`roster/orchestration/src/role_metadata.py`", "the bundled role-metadata module"},
	{"roster/orchestration/src/role_metadata.py", "the bundled role-metadata module"},
	{"`roster/orchestration/test/test_selector.py`", "the bundled selector's own test suite"},
	{"roster/orchestration/test/test_selector.py", "the bundled selector's own test suite"},
	{"`roster/orchestration/test/fixtures/selection_golden_corpus.json`", "the bundled selector's golden-corpus fixtures"},
	{"`roster/orchestration/test/test_repository_health.py`", "the bundled repository-health test suite"},
	{"`roster/orchestration/test/test_role_metadata.py`", "the bundled role-metadata test suite"},
	{"`test_routing_coverage.py`", "the bundled routing-coverage test suite"},
	{"`plugin/tools/test_port_cline_agents.py`", "the bundled Cline port's own test suite"},
	{"`roster/runner-capabilities.json`", "the bundled runner-capabilities manifest"},
	{"roster/runner-capabilities.json", "the bundled runner-capabilities manifest"},
	{"`roster/runner-capabilities.schema.json`", "the bundled runner-capabilities manifest's schema"},
	{"`roster/shared/knowledge-use-policy.md`", "this project's knowledge-use-policy documentation"},
	{"roster/shared/knowledge-use-policy.md", "this project's knowledge-use-policy documentation"},
	{"`roster/shared/context-use-policy.md`", "this project's context-use-policy documentation"},
	{"roster/shared/context-use-policy.md", "this project's context-use-policy documentation"},
	{"`roster/shared/operating-principles.md`", "this project's operating-principles documentation"},
	{"roster/shared/operating-principles.md", "this project's operating-principles documentation"},
	{"`roster/shared/technology-standards.md`", "this project's technology-standards documentation"},
	{"roster/shared/technology-standards.md", "this project's technology-standards documentation"},
	{"`roster/workflows/debugging.md`", "this repository's debugging workflow doc"},
	{"roster/workflows/debugging.md", "this repository's debugging workflow doc"},
	{"`roster/workflows/knowledge-ingestion.md`", "this repository's knowledge-ingestion workflow doc"},
	{"roster/workflows/knowledge-ingestion.md", "this repository's knowledge-ingestion workflow doc"},
	{"`roster/workflows/`", "this repository's worked-example workflow docs"},
	{"roster/workflows/", "this repository's worked-example workflow docs"},
}

var clineRoleOverrides = map[string][]substitution{
	"knowledge-store-steward": {
		{"by default (`SECURITY.md`), so also verify", "by default (see this project's security documentation for the exact default-resolution behavior), so also verify"},
	},
	"debugging-engineer": {
		{"When inspecting agents, verify `AGENT.md` authority, catalog registration, routing rules, knowledge focus, workflow alignment, selector tests, and runbook examples.", "When inspecting agents, verify the agent definition's authority, catalog/registry registration, routing rules, knowledge focus, workflow alignment, selector tests, and usage/runbook examples."},
	},
}

var clineToolMap = map[string]string{
	"Read":  "read_files",
	"Grep":  "search_codebase",
	"Glob":  "search_codebase",
	"Bash":  "run_commands",
	"Edit":  "editor",
	"Write": "editor",
}

var clineSkillLeakAllowlist = []string{
	".cline/roster/*.yml",
	"cline-roster/",
	"~/.cline/roster/",
}

const clineApplicationEngineerNote = "\n\n---\n\n_Port note (not part of the original role authority text): application-engineer's role text describes maintaining THIS deagy/cadre monorepo's own tooling (roster/catalog.yaml, roster/orchestration/routing.json, roster/RUNBOOK.md, the packaged-plugin regeneration flow via `cadre generate-plugin`/`cadre generate-role-metadata`, plugin/). Those are the literal subject of the role, not incidental cross-references, so they were left unrewritten; this preset is only meaningful when dispatched against a checkout of the deagy/cadre register repository itself, not an arbitrary consumer project._"

const clinePackagingNote = "> Cline packaging note: this skill's instructions describe this repository's own `roster/`-layout tooling in the abstract (the role catalog, routing configuration, and selector this plugin bundles) -- they are not literal paths to look up in an arbitrary target project. When dispatching, use `start_subagent`/`dispatch_selected_roles`/`bin/cadre select` rather than reading these files directly.\n\n"
