<!-- GENERATED FILE: edit the canonical source and regenerate; do not edit this copy. -->

# Repository Guidelines

## Project Structure & Module Organization

Agent roles, policies, workflows, orchestration, testing, support/escalation, and the knowledge store live under `roster/`; publishable repository skills live under `.agents/skills/`, with thin per-skill pointer files under `.claude/skills/` (Claude Code). A single general pointer file under `.clinerules/` (Cline CLI) references `AGENTS.md`/`roster/RUNBOOK.md` directly — it is unrelated to the per-skill pointer mechanism. The installable plugin distributions live in this same repository, under `plugin/`: the packaged Claude Code / Codex plugin generated from `roster/catalog.yaml`, `roster/`, `.agents/skills/` and this repository's `provider/` bundle, plus the three lifecycle plugin distributions (`cadre-lifecycle-core`/`-github`/`-gitlab`), plus a hand-authored TypeScript Cline CLI plugin under `cline-plugins/cline/`. The portable lifecycle kernel itself lives in this repository too, under `kernel/` (with the LangGraph orchestration engine that drives it under `engine/`).

Read `roster/RUNBOOK.md` for orchestration and any project-local `AGENTS.md` before product changes. Keep role definitions and `roster/catalog.yaml` synchronized.

## Build, Test, and Development Commands

Resolve Python 3.10+ as documented in the runbook. From each internal-tool component, run:

```powershell
<python> -B -m unittest discover -s test -p "test_*.py"
```

After changing `roster/catalog.yaml`, `roster/`, or `.agents/skills/`, regenerate derived output before committing. `git add` any new files **first** — the generator copies git-tracked files and silently skips untracked ones, so regenerating before staging ships a package referencing a file it does not contain:

```sh
./bin/cadre generate-authority-aides   # only when editing roster/authority/aides.yaml or _template.md.tmpl
./bin/cadre generate-role-metadata     # roster/catalog.yaml, routing's knowledge_focus, generated half of provider/
./bin/cadre generate-plugin --output plugin
python3 plugin/tools/port_cline_agents.py --root cline-plugins --source plugin
```

The order is load-bearing: `generate-plugin` copies `catalog.yaml` rather than deriving it, so it ships a stale catalog if `generate-role-metadata` has not run; and `port_cline_agents.py` reads the freshly written `plugin/` tree, so it must run last. `generate-plugin` never touches `cline-plugins/` — the Cline port is a genuinely separate command, and stopping before it leaves `cline-plugins/cline-agents/agents/<role>.md` diverged from its source.

This applies to code, not just role definitions: `plugin/suite/` bundles a copy of `roster/`, so a module added under `roster/*/src/` is part of the packaged CLI. It applies to this file too — `plugin/suite/AGENTS.md` is generated from this one. (Root `CLAUDE.md` is *not* bundled, and `plugin/AGENTS.md`/`plugin/CLAUDE.md` are hand-authored documents about `plugin/` itself, not copies of these.) Rather than memorising a list, run the commands whenever a change touches anything under `roster/`, `.agents/skills/`, or this file.

Then re-run the guards: `roster/orchestration/test/test_repository_health.py` and `python3 -m unittest discover -s plugin/tools -p "test_*.py"` — their coverage overlaps, so run both rather than reasoning about which one owns a given failure. `.github/workflows/validate.yml` re-runs `generate-plugin --check` in its `generated-content` job and the Cline byte-for-byte comparison in its `plugin-tools` job, so drift cannot outlive a pull request. Run lifecycle integration tests against the in-tree `kernel/`.

`roster/RUNBOOK.md` §17 is the canonical, worked-example version of this procedure; prefer extending it over restating it here.

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
