# Repository Guidelines

## Project Structure & Module Organization

Agent roles, policies, workflows, orchestration, testing, support/escalation, and the knowledge store live under `roster/`; publishable repository skills live under `.agents/skills/`, with thin per-skill pointer files under `.claude/skills/` (Claude Code). A single general pointer file under `.clinerules/` (Cline CLI) references `AGENTS.md`/`roster/RUNBOOK.md` directly — it is unrelated to the per-skill pointer mechanism. The installable plugin distributions live in this same repository, under `plugin/`: the packaged Claude Code / Codex plugin generated from `roster/catalog.yaml`, `roster/`, `.agents/skills/` and this repository's `provider/` bundle, plus the three lifecycle plugin distributions (`cadre-lifecycle-core`/`-github`/`-gitlab`), plus a hand-authored TypeScript Cline CLI plugin under `cline-plugins/cline/`. The portable lifecycle kernel itself lives in this repository too, under `kernel/` (with the LangGraph orchestration engine that drives it under `engine/`).

Read `roster/RUNBOOK.md` for orchestration and any project-local `AGENTS.md` before product changes. Keep role definitions and `roster/catalog.yaml` synchronized.

## Build, Test, and Development Commands

Resolve Python 3.10+ as documented in the runbook. From each internal-tool component, run:

```powershell
<python> -B -m unittest discover -s test -p "test_*.py"
```

After changing `roster/catalog.yaml`, `roster/`, or `.agents/skills/`, three regeneration steps are required, each with its own CI guard. Running only the first is the most common way to leave a PR red:

```sh
cadre generate-role-metadata                      # roster/catalog.yaml, routing's knowledge_focus, generated half of provider/
cadre generate-plugin --output plugin             # the packaged plugin, in this same repository
python3 plugin/tools/port_cline_agents.py --root cline-plugins --source plugin   # the Cline mirror
```

`generate-plugin` does **not** update `cline-plugins/`; the Cline port is a separate step, so a role edit that stops after step two leaves `cline-plugins/cline-agents/agents/<role>.md` diverged from its source. Commit all regenerated output alongside the source change.

This also applies to code, not just role definitions: `plugin/suite/` bundles a copy of `roster/`, so adding a module under `roster/knowledge-store/src/` without regenerating ships a plugin whose own CLI cannot import it — which fails as a `ModuleNotFoundError` in the `cline-agents` npm tests, a long way from the edit that caused it.

It applies to this file too. `plugin/AGENTS.md`, `plugin/CLAUDE.md`, and `plugin/suite/AGENTS.md` are generated from the repository-root `AGENTS.md`/`CLAUDE.md`, so editing either of those documents — including editing them to describe this very rule — requires the same regeneration pass. Treat "did I touch anything `plugin/` copies?" as the trigger, rather than trying to remember a list of directories.

Then re-run the guards: `roster/orchestration/test/test_repository_health.py` (catalog/role drift) and `python3 -m unittest discover -s plugin/tools -p "test_*.py"` (packaging, docs guards, and the Cline mirror's byte-for-byte match against source). `.github/workflows/validate.yml`'s `generated-content` job re-runs `generate-plugin --check` so drift cannot outlive a pull request. Run lifecycle integration tests against the in-tree `kernel/`.

For Go services, use `gofmt`, `go tool goimports`, `go vet ./...`, `go test ./...`, `go test -race ./...`, and `go tool golangci-lint run ./...`. For React frontends, use the project-pinned package manager for install, test, typecheck, and build commands. Podman, PostgreSQL migrations, Helm, and OpenTofu remain disposable or validation-only unless a project has explicit production approval; follow the component README and never target a persistent environment without approval.

The only Node/TypeScript source in this repository is the Cline CLI plugin under `cline-plugins/`, a separate npm workspace kept out of `plugin/` deliberately (see `README.md`'s repository layout notes).

## Coding Style & Naming Conventions

Use four-space indentation and snake_case for Python. Format Go with `gofmt` and `goimports`; lint with the committed `golangci-lint` config. Keep Go packages lowercase and errors safe for callers. Use two spaces, strict TypeScript, semantic React markup, CSS Modules, and lowercase kebab-case directories. Prefer the Go libraries and tools in `roster/shared/library-standards.yaml`; pin and justify every added dependency.

## Testing Guidelines

Use `unittest` for internal Python tools, Go `testing` plus Testify for services, and Vitest/Testing Library for React. Express integration and regression behavior in Gherkin/Godog. Cover authorization, negative paths, state transitions, accessibility, failure recovery, migrations, and sensitive-data exclusion. Use synthetic fixtures only.

## Commit & Merge Request Guidelines

Use short imperative commit subjects and focused changes. This GitHub-hosted
repository uses GitHub pull requests and GitHub Actions; each pull request must
describe scope, security implications, validation, affected decisions, and
linked issues. The Secure Cloud target profile may use GitLab merge requests
for downstream projects, but that is not this repository's contribution
workflow. Include CLI or UI evidence when behavior changes.

Never commit secrets, raw chat exports, real documents, local environment files, databases, object data, generated credentials, OpenTofu/Terraform state, or rendered secrets. Preserve independent review and human gates for persistent mutations, production, risk acceptance, and release.

## Agentic SDLC boundary

This repository is a provider/plugin distribution — it supplies provider
resources and dispatch inputs to *other* consuming projects, which own their
own `.agentic-sdlc/` overlays, run records, gate approvals, and lifecycle
decisions. This repository does not run its own `.agentic-sdlc/` overlay.

Do not copy lifecycle schemas, run-record validators, gate authorities, or
kernel authority into this repository. Never infer gate approval,
production/destructive authority, risk acceptance, or compliance applicability
for another project. Artifact authors must remain separate from independent
reviewers and human approvers.
