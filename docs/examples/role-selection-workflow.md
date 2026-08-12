# Role Selection Workflow

This example shows how to use Cadre's deterministic role selection to find the right specialist agents for a task.

## Scenario

A developer needs to add a new API endpoint to a Go microservice that handles user authentication. The task involves backend engineering, security review, and API contract design.

## Step 1: Run Role Selection

```sh
./bin/cadre select \
  --task "Add OAuth2 token refresh endpoint to auth-service Go microservice" \
  --files src/auth-service/api.go \
  --task-id AUTH-2026-001 \
  --classification internal
```

## Step 2: Inspect the Dispatch Plan

The selector returns a deterministic plan with:

- **Primary roles**: backend-engineer, api-contract-engineer (the agents that will do the work)
- **Reviewer roles**: security-reviewer, code-reviewer (independent review)
- **Support roles**: evidence-curator (records the work)
- **Dispatch precedence**: order in which roles should be engaged
- **Routing rationale**: why each role was selected (path/keyword matching)

## Step 3: Review the Plan

Before dispatching, review:

1. **Role coverage**: Does the plan include all necessary capabilities?
2. **Model assignments**: Are the model tiers appropriate? (e.g., `opus` for architecture, `sonnet` for implementation)
3. **Routing rationale**: Does the reasoning match the task?

If the plan needs adjustment, modify `roster/orchestration/routing.json` or the task description and re-run selection.

## Step 4: Dispatch Agents

Once the plan is approved, dispatch agents using the selected roles. Each agent receives:

- A task brief from `roster/orchestration/task-brief-template.md`
- The relevant role definition from `roster/<phase>/<role>/AGENT.md`
- Context from the knowledge store (if available)

## Step 5: Handle Escalation

If an agent encounters a situation requiring human judgment:

1. The agent follows the escalation policy in `roster/orchestration/escalation-policy.md`
2. The escalation-manager role routes to the appropriate authority aide
3. Human approval is required before proceeding

## Standalone vs. Integrated Mode

`cadre select` works standalone by default. For integrated mode with lifecycle gates:

```sh
# Requires the standalone agentic-sdlc CLI
AGENTIC_SDLC_BIN=/path/to/agentic-sdlc \
  ./bin/cadre select \
  --task "..." \
  --require-sdlc
```

In integrated mode, the dispatch plan includes `required_quality_gates` and `human_gates` annotations.

## See Also

- [docs/orchestration.md](../orchestration.md) — detailed orchestration guide
- [roster/RUNBOOK.md](../../roster/RUNBOOK.md) — complete operating reference
- [bin/README.md](https://github.com/deagy/cadre/blob/main/bin/README.md) — CLI dispatch mechanism. Absolute on purpose: `bin/README.md` documents this *register's* `bin/` layout (`cadre.py`, `cadre.ps1`, `subcommands.tsv`) and is deliberately not packaged, since the generated plugin ships a single POSIX-sh `bin/cadre` instead. A relative link would dangle in the package.
